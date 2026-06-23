package contributors

import "testing"

// findCluster returns the cluster that contains the given (kind,value), or nil.
func findCluster(clusters []Cluster, kind, value string) *Cluster {
	for i := range clusters {
		for _, id := range clusters[i].Identities {
			if id.Kind == kind && id.Value == value {
				return &clusters[i]
			}
		}
	}
	return nil
}

// sameCluster reports whether two identities landed in the same cluster.
func sameCluster(clusters []Cluster, k1, v1, k2, v2 string) bool {
	a := findCluster(clusters, k1, v1)
	b := findCluster(clusters, k2, v2)
	if a == nil || b == nil {
		return false
	}
	// Pointer identity: both point into the same slice element only if equal.
	return a == b || clusterKey(*a) == clusterKey(*b)
}

func clusterKey(c Cluster) string {
	min := ""
	for _, id := range c.Identities {
		k := id.Kind + ":" + id.Value
		if min == "" || k < min {
			min = k
		}
	}
	return min
}

func id(kind, value, name string, n int) Identity {
	return Identity{Kind: kind, Value: value, Name: name, NameSeen: name, Count: n}
}

func TestCluster_SameLoginDifferentEmails(t *testing.T) {
	// slinkiecici: gmail + github noreply, same login.
	ids := []Identity{
		id(KindLogin, "slinkiecici", "slinkiecici", 100),
		id(KindEmail, "slinkiecici@gmail.com", "slinkiecici", 50),
		id(KindEmail, "66627825+slinkiecici@users.noreply.github.com", "slinkiecici", 40),
	}
	cl := ClusterIdentities(ids)
	if !sameCluster(cl, KindEmail, "slinkiecici@gmail.com", KindLogin, "slinkiecici") {
		t.Fatalf("gmail email and login should cluster (email local-part == login)")
	}
	c := findCluster(cl, KindLogin, "slinkiecici")
	if c.PrimaryEmail != "slinkiecici@gmail.com" {
		t.Errorf("primary email = %q, want the real gmail (not the noreply)", c.PrimaryEmail)
	}
}

func TestCluster_EmailLocalPartEqualsLogin(t *testing.T) {
	ids := []Identity{
		id(KindLogin, "jsmith", "jsmith", 10),
		id(KindEmail, "jsmith@corp.com", "", 5),
	}
	cl := ClusterIdentities(ids)
	if !sameCluster(cl, KindLogin, "jsmith", KindEmail, "jsmith@corp.com") {
		t.Fatalf("login jsmith and jsmith@corp.com should cluster")
	}
}

func TestCluster_SameLocalPartAcrossDomains(t *testing.T) {
	// jane@gmail.com ↔ jane@corp.com — local part "jane" is len>=4 and not weak.
	ids := []Identity{
		id(KindEmail, "jane@gmail.com", "", 5),
		id(KindEmail, "jane@corp.com", "", 5),
	}
	cl := ClusterIdentities(ids)
	if !sameCluster(cl, KindEmail, "jane@gmail.com", KindEmail, "jane@corp.com") {
		t.Fatalf("same unique local-part across domains should cluster")
	}
}

func TestCluster_LocalPartGuard_ShortAndWeak(t *testing.T) {
	// "dev" is a weak local part → must NOT cluster across domains.
	weak := []Identity{
		id(KindEmail, "dev@a.com", "", 5),
		id(KindEmail, "dev@b.com", "", 5),
	}
	cl := ClusterIdentities(weak)
	if sameCluster(cl, KindEmail, "dev@a.com", KindEmail, "dev@b.com") {
		t.Errorf("weak local-part 'dev' should NOT cluster across domains")
	}

	// "abc" is too short (len < 4) → must NOT cluster.
	short := []Identity{
		id(KindEmail, "abc@a.com", "", 5),
		id(KindEmail, "abc@b.com", "", 5),
	}
	cl2 := ClusterIdentities(short)
	if sameCluster(cl2, KindEmail, "abc@a.com", KindEmail, "abc@b.com") {
		t.Errorf("short local-part 'abc' should NOT cluster across domains")
	}

	// numeric local part should NOT cluster across domains.
	num := []Identity{
		id(KindEmail, "12345@a.com", "", 5),
		id(KindEmail, "12345@b.com", "", 5),
	}
	cl3 := ClusterIdentities(num)
	if sameCluster(cl3, KindEmail, "12345@a.com", KindEmail, "12345@b.com") {
		t.Errorf("numeric local-part should NOT cluster across domains")
	}
}

func TestCluster_SameDisplayName(t *testing.T) {
	// Two distinct logins/emails sharing a real display name → one person.
	ids := []Identity{
		id(KindLogin, "jase", "Jase Strauss", 10),
		id(KindLogin, "jase-strauss", "Jase Strauss", 8),
	}
	cl := ClusterIdentities(ids)
	if !sameCluster(cl, KindLogin, "jase", KindLogin, "jase-strauss") {
		t.Fatalf("identities with same display name should cluster")
	}
}

func TestCluster_GenericNamesDoNotMerge(t *testing.T) {
	// "root"/"github"/"unknown" are generic — must NOT union unrelated people.
	ids := []Identity{
		id(KindEmail, "alice@x.com", "root", 3),
		id(KindEmail, "bob@y.com", "root", 3),
		id(KindLogin, "carol", "github", 2),
		id(KindLogin, "dave", "github", 2),
	}
	cl := ClusterIdentities(ids)
	if sameCluster(cl, KindEmail, "alice@x.com", KindEmail, "bob@y.com") {
		t.Errorf("generic name 'root' must not merge alice & bob")
	}
	if sameCluster(cl, KindLogin, "carol", KindLogin, "dave") {
		t.Errorf("generic name 'github' must not merge carol & dave")
	}
}

func TestCluster_BotDetection(t *testing.T) {
	cases := []struct {
		idv  Identity
		want bool
	}{
		{id(KindLogin, "dependabot[bot]", "dependabot[bot]", 1), true},
		{id(KindEmail, "49699333+dependabot[bot]@users.noreply.github.com", "dependabot[bot]", 1), true},
		{id(KindEmail, "noreply@anthropic.com", "claude", 1), true},
		{id(KindLogin, "renovate[bot]", "renovate", 1), true},
		{id(KindLogin, "github-actions[bot]", "github-actions", 1), true},
		{id(KindLogin, "jsmith", "jsmith", 1), false},
		{id(KindEmail, "jane@gmail.com", "Jane", 1), false},
	}
	for _, c := range cases {
		if got := IsBotIdentity(c.idv); got != c.want {
			t.Errorf("IsBotIdentity(%s:%s) = %v, want %v", c.idv.Kind, c.idv.Value, got, c.want)
		}
	}

	// A cluster containing a bot identity is flagged is_bot.
	cl := ClusterIdentities([]Identity{
		id(KindLogin, "dependabot[bot]", "dependabot[bot]", 5),
		id(KindEmail, "49699333+dependabot[bot]@users.noreply.github.com", "dependabot[bot]", 5),
	})
	c := findCluster(cl, KindLogin, "dependabot[bot]")
	if c == nil || !c.IsBot {
		t.Errorf("dependabot cluster should be flagged is_bot")
	}
}

func TestCluster_PrimaryEmailPrefersReal(t *testing.T) {
	// noreply / .local / example.com are not "real" — the gmail should win.
	ids := []Identity{
		id(KindLogin, "cameron", "cameron", 100),
		id(KindEmail, "cameron@camerons-macbook-pro.local", "cameron", 80),
		id(KindEmail, "cameron@192-168-68-58.local", "cameron", 50),
		id(KindEmail, "cameron@realmail.com", "cameron", 30),
	}
	cl := ClusterIdentities(ids)
	c := findCluster(cl, KindLogin, "cameron")
	if c.PrimaryEmail != "cameron@realmail.com" {
		t.Errorf("primary email = %q, want cameron@realmail.com (non-.local)", c.PrimaryEmail)
	}
}

func TestCluster_DisplayNameMostFrequent(t *testing.T) {
	ids := []Identity{
		id(KindLogin, "jdoe", "John Doe", 100),
		id(KindEmail, "jdoe@x.com", "J. Doe", 5),
	}
	cl := ClusterIdentities(ids)
	c := findCluster(cl, KindLogin, "jdoe")
	if c.DisplayName != "John Doe" {
		t.Errorf("display name = %q, want most-frequent 'John Doe'", c.DisplayName)
	}
}

func TestCluster_DistinctPeopleStaySeparate(t *testing.T) {
	ids := []Identity{
		id(KindLogin, "alice", "Alice", 10),
		id(KindEmail, "alice@x.com", "Alice", 8),
		id(KindLogin, "bob", "Bob", 9),
		id(KindEmail, "bob@y.com", "Bob", 7),
	}
	cl := ClusterIdentities(ids)
	if len(cl) != 2 {
		t.Fatalf("want 2 clusters (alice, bob), got %d", len(cl))
	}
	if sameCluster(cl, KindLogin, "alice", KindLogin, "bob") {
		t.Errorf("alice and bob must not cluster")
	}
}
