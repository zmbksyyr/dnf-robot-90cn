package runtime

import (
	"bytes"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"io/fs"
	"os"
	"reflect"
	goruntime "runtime"
	"testing"

	"robot/internal/capability/catalog"
	"robot/internal/capability/robotconfig"
	storecap "robot/internal/capability/store"
	"robot/internal/foundation/config"
	"robot/internal/foundation/layout"
)

func TestInitRejectsEmptyRuntimeDirectory(t *testing.T) {
	if err := Init(&config.SysConfig{}); err == nil {
		t.Fatal("empty runtime directory unexpectedly accepted")
	}
}

func TestInitConfigOnlyDoesNotRequirePVF(t *testing.T) {
	dir := t.TempDir()
	if err := InitConfigOnly(&config.SysConfig{ConfigDir: dir}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(layout.New(dir).RobotConfig()); err != nil {
		t.Fatalf("robot config was not initialized: %v", err)
	}
}

func TestReleaseDefaultsCoversCanonicalRuntimeAssets(t *testing.T) {
	paths := layout.New(t.TempDir())
	if err := paths.Ensure(); err != nil {
		t.Fatal(err)
	}
	if err := releaseDefaults(paths); err != nil {
		t.Fatal(err)
	}

	expected := map[string]string{
		"compat.json":                paths.MailboxGuard(),
		"party_compat.json":          paths.PartyCompatibility(),
		"party_skill_catalog.json":   paths.PartySkills(),
		"privatekey.pem":             paths.PrivateKey(),
		"publickey.pem":              paths.PublicKey(),
		"robot_config.ini":           paths.RobotConfig(),
		"robot_name_templates.json":  paths.NameTemplates(),
		"robot_shout_templates.json": paths.ShoutTemplates(),
		"robot_store_titles.json":    paths.StoreTitles(),
	}
	entries, err := fs.ReadDir(defaultFiles, "defaults")
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != len(expected) {
		t.Fatalf("embedded runtime assets=%d, categorized destinations=%d", len(entries), len(expected))
	}
	for _, entry := range entries {
		if entry.IsDir() {
			t.Fatalf("unexpected embedded runtime asset directory: %s", entry.Name())
		}
		dst, ok := expected[entry.Name()]
		if !ok {
			t.Fatalf("embedded runtime asset has no asserted destination: %s", entry.Name())
		}
		data, err := os.ReadFile(dst)
		if err != nil {
			t.Fatalf("read released runtime file %s: %v", dst, err)
		}
		if len(data) == 0 {
			t.Fatalf("released runtime file is empty: %s", dst)
		}
		embedded, err := defaultFiles.ReadFile("defaults/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(data, embedded) {
			t.Fatalf("released runtime file differs from embedded asset: %s", dst)
		}
	}

	assertReleasedRuntimeAssetsParse(t, paths)
	if goruntime.GOOS != "windows" {
		privateInfo, err := os.Stat(paths.PrivateKey())
		if err != nil {
			t.Fatalf("stat released private key: %v", err)
		}
		if got := privateInfo.Mode().Perm(); got != 0600 {
			t.Fatalf("released private key mode = %o, want 600", got)
		}
	}
	rootEntries, err := os.ReadDir(paths.Root)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range rootEntries {
		if !entry.IsDir() {
			t.Fatalf("uncategorized runtime file released at config root: %s", entry.Name())
		}
	}
}

func assertReleasedRuntimeAssetsParse(t *testing.T, paths layout.Paths) {
	t.Helper()
	runtimeConfig, err := robotconfig.LoadFile(paths.RobotConfig())
	if err != nil {
		t.Fatalf("released robot config is invalid: %v", err)
	}
	if !reflect.DeepEqual(runtimeConfig, robotconfig.Default()) {
		t.Fatalf("released robot config defaults drifted from code defaults: released=%+v code=%+v", runtimeConfig, robotconfig.Default())
	}
	if _, err := catalog.ReadNameTemplates(paths.NameTemplates()); err != nil {
		t.Fatalf("released name templates are invalid: %v", err)
	}
	if _, err := catalog.ReadShoutTemplates(paths.ShoutTemplates()); err != nil {
		t.Fatalf("released shout templates are invalid: %v", err)
	}
	partySkills, err := catalog.ReadPartySkillCatalog(paths.PartySkills())
	if err != nil {
		t.Fatalf("released party skill catalog is invalid: %v", err)
	}
	if len(partySkills.Issues) != 0 {
		t.Fatalf("released party skill catalog has semantic issues: %+v", partySkills.Issues)
	}
	storeTitles, err := storecap.LoadTitleCatalog(paths.StoreTitles())
	if err != nil {
		t.Fatalf("released store titles are invalid: %v", err)
	}
	if storeTitles.Len() == 0 {
		t.Fatal("released store titles contain no usable title")
	}

	var mailbox struct {
		Enabled *bool `json:"mailbox_bad_node_guard"`
	}
	decodeReleasedJSON(t, paths.MailboxGuard(), &mailbox)
	if mailbox.Enabled == nil {
		t.Fatal("released mailbox compatibility config is incomplete")
	}
	var partyCompat struct {
		Enabled      *bool   `json:"enabled"`
		AccountStart *uint32 `json:"account_start"`
		AccountEnd   *uint32 `json:"account_end"`
	}
	decodeReleasedJSON(t, paths.PartyCompatibility(), &partyCompat)
	if partyCompat.Enabled == nil || partyCompat.AccountStart == nil || partyCompat.AccountEnd == nil ||
		*partyCompat.AccountStart == 0 || *partyCompat.AccountStart >= *partyCompat.AccountEnd {
		t.Fatal("released party compatibility config is incomplete or invalid")
	}

	privateBlock := decodeReleasedPEM(t, paths.PrivateKey(), "RSA PRIVATE KEY")
	privateKey, err := x509.ParsePKCS1PrivateKey(privateBlock.Bytes)
	if err != nil {
		t.Fatalf("released private key is invalid: %v", err)
	}
	publicBlock := decodeReleasedPEM(t, paths.PublicKey(), "PUBLIC KEY")
	parsedPublic, err := x509.ParsePKIXPublicKey(publicBlock.Bytes)
	if err != nil {
		t.Fatalf("released public key is invalid: %v", err)
	}
	publicKey, ok := parsedPublic.(*rsa.PublicKey)
	if !ok || privateKey.PublicKey.E != publicKey.E || privateKey.PublicKey.N.Cmp(publicKey.N) != 0 {
		t.Fatal("released private and public keys do not form a pair")
	}
}

func decodeReleasedJSON(t *testing.T, path string, dst any) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := config.DecodeJSONBytes(data, dst); err != nil {
		t.Fatalf("released JSON %s is invalid: %v", path, err)
	}
}

func decodeReleasedPEM(t *testing.T, path, wantType string) *pem.Block {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, rest := pem.Decode(data)
	if block == nil || block.Type != wantType || len(bytes.TrimSpace(rest)) != 0 {
		t.Fatalf("released PEM %s is invalid or has type %q", path, blockType(block))
	}
	return block
}

func blockType(block *pem.Block) string {
	if block == nil {
		return ""
	}
	return block.Type
}
