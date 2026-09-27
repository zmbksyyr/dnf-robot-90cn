package webadmin

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedAssetsContainCoreS4A21UI(t *testing.T) {
	checks := []struct {
		name, content string
		required      []string
	}{
		{"index", indexHTML, []string{"openAutoDialog", "openBackendDialog", "runAction('robotsMove')", "runAction('robotsShout')"}},
		{"javascript", appJS, []string{"robotsOnlineAsync", "robotsMove", "robotsShout", "robotsLogoutAsync", "cleanupRobotsAsync", "dangerousDeleteAsync", "backendCapabilities", "stopRobot"}},
		{"i18n", i18nJS, []string{"I18N_MESSAGES", "toggleLanguage", "auto.target_online", "auto.shout_interval", "backend.recovery"}},
	}
	for _, check := range checks {
		for _, want := range check.required {
			if !strings.Contains(check.content, want) {
				t.Errorf("%s is missing %q", check.name, want)
			}
		}
	}
}

func TestRemovedAndUnimplementedWebSurfacesAreAbsent(t *testing.T) {
	content := indexHTML + appJS + i18nJS + appCSS
	for _, token := range []string{"openCompatDialog", "openKeyDialog", "openDiagnosticsDialog", "openMaxDialog", "openScriptDialog", "openPortsDialog", "portsButton", "game-endpoint", "submitMarketSettings", "market.", "dialog.market", "party-debug", "service-ports", "monitorPort", "auctionPort", "pointPort", "relayPort", "70 Compat", "RSA Key"} {
		if strings.Contains(content, token) {
			t.Errorf("obsolete web surface remains: %q", token)
		}
	}
}

func TestAutoDialogUsesBackendCapacityAndCapabilities(t *testing.T) {
	for _, want := range []string{"backendMaxOnline||10000", "backendCapabilities.mail_notification", "backendCapabilities.system_announcement", "backendCapabilities.dungeon_follow", "auto.auto_target_online_count", "auto.auto_shout_interval_min_sec", "auto.auto_shout_interval_max_sec", "auto.auto_system_announcement"} {
		if !strings.Contains(appJS, want) {
			t.Errorf("auto boundary is missing %q", want)
		}
	}
}

func TestDatabaseCardUsesDashboardProjection(t *testing.T) {
	for _, want := range []string{"api('dashboardStatus')", "backendCapabilities.database", "String(r.engine).toUpperCase()", "r.writable?' · '+i18nFormat('database.writable'):''"} {
		if !strings.Contains(appJS, want) {
			t.Errorf("database projection is missing %q", want)
		}
	}
}

func TestIndexTemplateInlinesEmbeddedAssets(t *testing.T) {
	var rendered bytes.Buffer
	if err := cleanIndexTemplate.Execute(&rendered, nil); err != nil {
		t.Fatal(err)
	}
	page := rendered.String()
	for _, placeholder := range []string{appCSSPlaceholder, i18nJSPlaceholder, appJSPlaceholder} {
		if strings.Contains(page, placeholder) {
			t.Errorf("rendered index contains %q", placeholder)
		}
	}
	for _, asset := range []string{trimAssetTerminator(appCSS), trimAssetTerminator(i18nJS), trimAssetTerminator(appJS)} {
		if !strings.Contains(page, asset) {
			t.Fatal("rendered index is missing an embedded asset")
		}
	}
}

func TestLoginTemplateEscapesErrorAndSupportsRecovery(t *testing.T) {
	const loginError = `<script>alert("bad")</script>`
	var rendered bytes.Buffer
	if err := cleanLoginTemplate.Execute(&rendered, map[string]any{"Error": loginError, "Recovery": true}); err != nil {
		t.Fatal(err)
	}
	page := rendered.String()
	if strings.Contains(page, loginError) || !strings.Contains(page, "&lt;script&gt;") || !strings.Contains(page, `data-i18n="login.recovery"`) {
		t.Fatal("login template does not escape errors or expose recovery")
	}
}

func TestI18nLocalesHaveMatchingKeys(t *testing.T) {
	parts := strings.SplitN(i18nJS, "\n},\nzh:{\n", 2)
	if len(parts) != 2 {
		t.Fatal("cannot split locales")
	}
	re := regexp.MustCompile(`'([a-zA-Z0-9_.]+)':`)
	keys := func(s string) map[string]bool {
		m := map[string]bool{}
		for _, x := range re.FindAllStringSubmatch(s, -1) {
			m[x[1]] = true
		}
		return m
	}
	zhTable := strings.SplitN(parts[1], "\n}};", 2)[0]
	en, zh := keys(parts[0]), keys(zhTable)
	for key := range en {
		if !zh[key] {
			t.Errorf("Chinese locale is missing %q", key)
		}
	}
	for key := range zh {
		if !en[key] {
			t.Errorf("English locale is missing %q", key)
		}
	}
}

func TestSchedulerAndActorStoreStatusAreLocalized(t *testing.T) {
	for _, want := range []string{`data-i18n="scheduler.policy_mode"`, "i18nFormat('scheduler.attach_value'", `data-i18n="robots.store"`, "i18nFormat('status.'+store)"} {
		if !strings.Contains(indexHTML+appJS, want) {
			t.Errorf("localized dashboard is missing %q", want)
		}
	}
	for _, stale := range []string{"i18nEnglishFormat", "data-i18n-skip"} {
		if strings.Contains(indexHTML+appJS, stale) {
			t.Errorf("dashboard still forces English via %q", stale)
		}
	}
}

func TestDashboardRenderingOnlyReferencesPresentElements(t *testing.T) {
	for _, want := range []string{`id="mRobot"`, `id="mDB"`, `class="scheduler"`, `id="sMode"`, `id="sReason"`} {
		if !strings.Contains(indexHTML, want) {
			t.Errorf("dashboard element is missing %q", want)
		}
	}
	for _, stale := range []string{"byId('mStore')", "byId('sStore')", `id="sStore"`} {
		if strings.Contains(indexHTML+appJS, stale) {
			t.Errorf("dashboard rendering still depends on removed element %q", stale)
		}
	}
}

func TestBackendRecoveryAndExplicitApplyFlowRemainAvailable(t *testing.T) {
	for _, want := range []string{"recovery_mode", "submitBackendSettings", "backendSettings", "backendCapabilities"} {
		if !strings.Contains(appJS, want) {
			t.Errorf("backend recovery flow is missing %q", want)
		}
	}
}
