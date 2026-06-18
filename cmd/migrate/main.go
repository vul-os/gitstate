// Command migrate is gitstate's Supabase-style migration runner.
//
// Migrations live in ./migrations as forward-only files named:
//
//	YYYYMMDD_NNN_name.sql
//
// There is no up/down — a rollback is a new forward migration. Files are applied
// in lexical order and tracked in schema_migrations.
//
// Usage:
//
//	go run ./cmd/migrate [--env dev] <command>
//
// Commands:
//
//	new <name>   scaffold migrations/<date>_<nnn>_<name>.sql
//	up           apply all pending migrations
//	status       list applied + pending migrations
//	version      print the latest applied version
//	reset        DROP everything and re-apply from scratch (refused unless env=dev)
package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
)

const migrationsDir = "migrations"

var fileRe = regexp.MustCompile(`^(\d{8}_\d{3})_([a-z0-9_]+)\.sql$`)

func main() {
	args := os.Args[1:]
	env := "dev"
	// optional --env <name>
	for i := 0; i < len(args); i++ {
		if args[i] == "--env" && i+1 < len(args) {
			env = args[i+1]
			args = append(args[:i], args[i+2:]...)
			break
		}
	}
	if len(args) == 0 {
		usage()
		os.Exit(2)
	}
	loadEnv(env)

	cmd := args[0]
	rest := args[1:]
	var err error
	switch cmd {
	case "new":
		err = cmdNew(rest)
	case "up":
		err = cmdUp()
	case "status":
		err = cmdStatus()
	case "version":
		err = cmdVersion()
	case "reset":
		err = cmdReset(env)
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprint(os.Stderr, `gitstate migrate — forward-only migrations

usage: migrate [--env dev|prod] <command>

  new <name>   scaffold a migration file
  up           apply all pending migrations
  status       show applied + pending
  version      latest applied version
  reset        drop & re-apply (dev only)
`)
}

// ── env loading ───────────────────────────────────────────────────────────
// Loads .env.<env> if present, then .env, without clobbering already-set vars.
func loadEnv(env string) {
	for _, f := range []string{".env." + env, ".env"} {
		applyEnvFile(f)
	}
}

func applyEnvFile(path string) {
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		if k == "" {
			continue
		}
		if _, exists := os.LookupEnv(k); !exists {
			_ = os.Setenv(k, v)
		}
	}
}

func dbURL() (string, error) {
	u := os.Getenv("DATABASE_URL")
	if u == "" {
		return "", errors.New("DATABASE_URL not set (check .env / .env.dev)")
	}
	return u, nil
}

func connect(ctx context.Context) (*pgx.Conn, error) {
	u, err := dbURL()
	if err != nil {
		return nil, err
	}
	return pgx.Connect(ctx, u)
}

// ── schema_migrations ─────────────────────────────────────────────────────
const ensureTable = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version    text PRIMARY KEY,
    name       text NOT NULL,
    checksum   text NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
);`

type migration struct {
	version  string // YYYYMMDD_NNN
	name     string
	path     string
	checksum string
	body     string
}

func loadMigrations() ([]migration, error) {
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		m := fileRe.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		body, err := os.ReadFile(filepath.Join(migrationsDir, e.Name()))
		if err != nil {
			return nil, err
		}
		sum := sha256.Sum256(body)
		out = append(out, migration{
			version:  m[1],
			name:     m[2],
			path:     filepath.Join(migrationsDir, e.Name()),
			checksum: hex.EncodeToString(sum[:]),
			body:     string(body),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	return out, nil
}

func appliedSet(ctx context.Context, c *pgx.Conn) (map[string]string, error) {
	if _, err := c.Exec(ctx, ensureTable); err != nil {
		return nil, err
	}
	rows, err := c.Query(ctx, `SELECT version, checksum FROM schema_migrations`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	set := map[string]string{}
	for rows.Next() {
		var v, ck string
		if err := rows.Scan(&v, &ck); err != nil {
			return nil, err
		}
		set[v] = ck
	}
	return set, rows.Err()
}

// ── commands ──────────────────────────────────────────────────────────────
func cmdNew(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: migrate new <name>")
	}
	name := strings.ToLower(strings.Join(args, "_"))
	name = regexp.MustCompile(`[^a-z0-9_]+`).ReplaceAllString(name, "_")
	name = strings.Trim(name, "_")
	if name == "" {
		return errors.New("invalid name")
	}
	if err := os.MkdirAll(migrationsDir, 0o755); err != nil {
		return err
	}
	existing, err := loadMigrations()
	if err != nil {
		return err
	}
	date := time.Now().Format("20060102")
	seq := 1
	for _, m := range existing {
		if strings.HasPrefix(m.version, date+"_") {
			if n, e := strconv.Atoi(strings.TrimPrefix(m.version, date+"_")); e == nil && n >= seq {
				seq = n + 1
			}
		}
	}
	version := fmt.Sprintf("%s_%03d", date, seq)
	path := filepath.Join(migrationsDir, version+"_"+name+".sql")
	tmpl := fmt.Sprintf("-- %s_%s\n-- forward-only; a rollback is a new migration.\n\n", version, name)
	if err := os.WriteFile(path, []byte(tmpl), 0o644); err != nil {
		return err
	}
	fmt.Println("created", path)
	return nil
}

func cmdUp() error {
	ctx := context.Background()
	c, err := connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close(ctx)

	migs, err := loadMigrations()
	if err != nil {
		return err
	}
	applied, err := appliedSet(ctx, c)
	if err != nil {
		return err
	}

	n := 0
	for _, m := range migs {
		if ck, ok := applied[m.version]; ok {
			if ck != m.checksum {
				return fmt.Errorf("checksum mismatch on already-applied %s — do not edit applied migrations; add a new one", m.version)
			}
			continue
		}
		tx, err := c.Begin(ctx)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(ctx, m.body); err != nil {
			_ = tx.Rollback(ctx)
			return fmt.Errorf("%s failed: %w", m.version, err)
		}
		if _, err := tx.Exec(ctx,
			`INSERT INTO schema_migrations (version, name, checksum) VALUES ($1,$2,$3)`,
			m.version, m.name, m.checksum); err != nil {
			_ = tx.Rollback(ctx)
			return err
		}
		if err := tx.Commit(ctx); err != nil {
			return err
		}
		fmt.Printf("applied %s_%s\n", m.version, m.name)
		n++
	}
	if n == 0 {
		fmt.Println("already up to date")
	} else {
		fmt.Printf("done — %d migration(s) applied\n", n)
	}
	return nil
}

func cmdStatus() error {
	ctx := context.Background()
	c, err := connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close(ctx)
	migs, err := loadMigrations()
	if err != nil {
		return err
	}
	applied, err := appliedSet(ctx, c)
	if err != nil {
		return err
	}
	for _, m := range migs {
		mark := "pending"
		if _, ok := applied[m.version]; ok {
			mark = "applied"
		}
		fmt.Printf("  [%s] %s_%s\n", mark, m.version, m.name)
	}
	if len(migs) == 0 {
		fmt.Println("  (no migrations)")
	}
	return nil
}

func cmdVersion() error {
	ctx := context.Background()
	c, err := connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close(ctx)
	if _, err := c.Exec(ctx, ensureTable); err != nil {
		return err
	}
	var v string
	err = c.QueryRow(ctx, `SELECT version FROM schema_migrations ORDER BY version DESC LIMIT 1`).Scan(&v)
	if errors.Is(err, pgx.ErrNoRows) {
		fmt.Println("(none applied)")
		return nil
	}
	if err != nil {
		return err
	}
	fmt.Println(v)
	return nil
}

func cmdReset(env string) error {
	if env != "dev" && os.Getenv("GITSTATE_ENV") != "dev" {
		return fmt.Errorf("reset refused: env=%q (only allowed when env=dev)", env)
	}
	ctx := context.Background()
	c, err := connect(ctx)
	if err != nil {
		return err
	}
	defer c.Close(ctx)
	fmt.Println("resetting public schema…")
	if _, err := c.Exec(ctx, `DROP SCHEMA public CASCADE; CREATE SCHEMA public;`); err != nil {
		return err
	}
	c.Close(ctx)
	return cmdUp()
}
