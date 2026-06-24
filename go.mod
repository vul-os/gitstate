module github.com/exo/gitstate

go 1.25.6

require (
	github.com/go-pdf/fpdf v0.9.0
	github.com/golang-jwt/jwt/v5 v5.2.2
	github.com/google/go-github/v66 v66.0.0
	github.com/jackc/pgx/v5 v5.10.0
	github.com/llmux/llmux v0.0.0-00010101000000-000000000000
	github.com/oschwald/maxminddb-golang/v2 v2.4.0
	gitlab.com/gitlab-org/api/client-go v1.46.0
	golang.org/x/crypto v0.31.0
	golang.org/x/oauth2 v0.34.0
	gopkg.in/yaml.v3 v3.0.1
)

require (
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/google/go-querystring v1.2.0 // indirect
	github.com/hashicorp/go-cleanhttp v0.5.2 // indirect
	github.com/hashicorp/go-retryablehttp v0.7.8 // indirect
	github.com/jackc/pgpassfile v1.0.0 // indirect
	github.com/jackc/pgservicefile v0.0.0-20240606120523-5a60cdf6a761 // indirect
	github.com/jackc/puddle/v2 v2.2.2 // indirect
	github.com/kr/text v0.2.0 // indirect
	github.com/redis/go-redis/v9 v9.20.1 // indirect
	github.com/rogpeppe/go-internal v1.15.0 // indirect
	go.uber.org/atomic v1.11.0 // indirect
	golang.org/x/sync v0.21.0 // indirect
	golang.org/x/sys v0.45.0 // indirect
	golang.org/x/text v0.38.0 // indirect
	golang.org/x/time v0.14.0 // indirect
)

replace github.com/llmux/llmux => ../../vulos/llmux
