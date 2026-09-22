package webadmin

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
)

func TestEmbeddedWebAssetsContainRequiredContent(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		required []string
	}{
		{name: "login", content: loginHTML, required: []string{"Robot Web", `action="/login"`, "{{if .Error}}", i18nJSPlaceholder, `id="languageButton"`}},
		{name: "index", content: indexHTML, required: []string{"TW Robot Web", appCSSPlaceholder, i18nJSPlaceholder, appJSPlaceholder, `id="languageButton"`, `id="partyCompatButton"`, `id="compatButton"`}},
		{name: "css", content: appCSS, required: []string{":root{", ".service-lights", ".diagrow", ".market-policy-select", ".market-rule-article", ".market-rule-details", ".market-rule-chevrons"}},
		{name: "i18n", content: i18nJS, required: []string{"I18N_MESSAGES", "tw_language", "toggleLanguage", "currentLanguage=localStorage.getItem(I18N_STORAGE_KEY)==='zh'?'zh':'en'", "auto.shout_interval", "喊话间隔", "validation.shout_interval", "market.section_status", "market.price_range_policy", "market.allowed_rarities", "上架稀有度（0-9）", "范围外回收概率"}},
		{name: "javascript", content: appJS, required: []string{"async function api(", "openPartyCompatDialog", "openCompatDialog", "openDiagnosticsDialog", "restartRobot", "autoMailNotify", "autoShoutMin", "autoShoutMax", "auto.auto_shout_interval_min_sec", "auto.auto_shout_interval_max_sec", "marketEquipmentRarities", "marketOtherRarities", "marketBlockedItemIDs", "parseBlockedItemIDExpression", "formatBlockedItemIDs", "marketAllowedItemIDs", "parseAllowedItemIDExpression", "formatAllowedItemIDs", "allowed_item_id_expression", "Allowed item IDs", "物品 ID 白名单", "normalizeRarityDigits", "equipment_allowed_rarities", "other_allowed_rarities", "blocked_item_id_expression", "marketEquipmentLevelMin", "marketDetailsFormSection", "marketCategoryPriceRules", "marketEquipmentExtras", "marketCommonPriceSettings", "category_price_rules", "equipment_multiplier_min", "equipment_multiplier_max", "equipment_final_max_price", "equipment_trade_policy", "other_trade_policy", "marketInRangeProbability", "marketApplyListingConfig", "marketKindsProgress", "种类（实际 / 预期）"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if strings.TrimSpace(tt.content) == "" {
				t.Fatal("embedded asset is empty")
			}
			for _, required := range tt.required {
				if !strings.Contains(tt.content, required) {
					t.Errorf("embedded asset is missing %q", required)
				}
			}
		})
	}
}

func TestAutoControlIsNotGatedByMarketCapability(t *testing.T) {
	if !strings.Contains(appJS, `async function openAutoDialog`) {
		t.Fatal("auto control is missing from the web asset")
	}
	if strings.Contains(appJS, `cmd.includes("openMaxDialog")||cmd.includes("openScriptDialog")||cmd.includes("openAutoDialog")`) {
		t.Fatal("auto control is incorrectly gated by market capability")
	}
}

func TestWebSeparatesLocalAndWorldShoutCapabilities(t *testing.T) {
	if !strings.Contains(appJS, `cmd.includes("robotsShoutLocal")`) || !strings.Contains(appJS, `cap='world_shout'`) {
		t.Fatal("web shout actions are not separated by capability")
	}
	if !strings.Contains(indexHTML, `data-i18n="action.shout_local"`) {
		t.Fatal("local shout button is missing")
	}
}

func TestWebLabelsMovementAsTownMovement(t *testing.T) {
	if !strings.Contains(indexHTML, `data-i18n="action.town_move"`) || !strings.Contains(indexHTML, `Town move`) {
		t.Fatal("movement action is not explicitly labeled as town movement")
	}
	if !strings.Contains(i18nJS, "'action.town_move':'Town move'") || !strings.Contains(i18nJS, "'action.town_move':'城镇移动'") {
		t.Fatal("town movement translations are missing")
	}
	if !strings.Contains(appJS, "robotsMove:'Town move'") {
		t.Fatal("action summary does not distinguish town movement")
	}
}

func TestMarketPricingAndRuleSummaryAreBilingual(t *testing.T) {
	for _, want := range []string{
		"Category unit-price ranges", "分类单价范围",
		"Unit price range", "单价范围",
		"Equipment price", "装备价格",
		"Equipment price multiplier", "装备价格倍率",
		"Other / Unclassified", "其他 / 未分类",
		"Final maximum unit price", "最终单价上限",
		"Upgrade price rate (nonlinear)", "强化加价率（非线性）",
		"Final price fluctuation", "最终价格浮动",
		"Rule details", "规则说明",
		"Data sources and boundary", "数据来源与边界",
		"Saving and rebuilding", "保存与重建",
		"Filtering order", "过滤顺序",
		"Restock planning", "补货规划",
		"Price calculation", "价格计算",
		"Execution and confirmation", "执行与确认",
		"Automatic recycling", "自动回收",
		"Automatic operation and recovery", "自动运行与恢复",
		"Manual actions", "手动操作",
		"intersection of the PVF auction catalog", "PVF 拍卖目录与当前已发布的 iteminfo.dat 的交集",
		"does not export, replace, or publish ItemInfo", "不会导出、替换或发布 ItemInfo",
		"paused temporarily and resumed after rebuilding", "先临时停止，并在重建结束后恢复",
		"allowlist take effect", "之后才处理白名单",
		"currently deployed iteminfo.dat", "当前已发布的 iteminfo.dat",
		"partially stocked ID is not topped up", "已有部分库存的 ID 不会继续补足",
		"titles, creatures, artifacts, avatars", "称号、宠物、宠物装备、时装",
		"item-specific price range has the highest priority", "启用物品独立价格范围时优先使用该范围",
		"checks the database again", "再次查询数据库",
		"player buyout listings", "玩家一口价商品",
		"total price divided by quantity", "总价除以数量作为单价",
		"reduce send pressure", "降低发送压力",
		"separate publishing workflow", "独立的发布流程",
	} {
		if !strings.Contains(appJS, want) {
			t.Errorf("market UI is missing bilingual text %q", want)
		}
	}
	for _, removed := range []string{"equipment_price_protection", "marketEquipmentPriceProtection", "level_price_rate", "rarity_price_rate", "value_model_enabled", "value_category_recognition", "value_curve_span", "value_base_price", "equip_inflate_min"} {
		if strings.Contains(appJS, removed) {
			t.Errorf("market UI still contains removed pricing setting %q", removed)
		}
	}
}

func TestMarketRuleDetailsAreCollapsedWithDoubleChevron(t *testing.T) {
	for _, want := range []string{
		`<details class="formsection market-rule-details"><summary>`,
		`class="market-rule-chevrons" aria-hidden="true"><i></i><i></i>`,
		`.market-rule-details[open] .market-rule-chevrons`,
	} {
		if !strings.Contains(appJS+appCSS, want) {
			t.Errorf("market rule disclosure is missing %q", want)
		}
	}
	if strings.Contains(appJS, `<details class="formsection market-rule-details" open`) {
		t.Fatal("market rule details must be collapsed by default")
	}
}

func TestIndexTemplateInlinesEmbeddedAssets(t *testing.T) {
	var rendered bytes.Buffer
	if err := cleanIndexTemplate.Execute(&rendered, nil); err != nil {
		t.Fatalf("execute index template: %v", err)
	}
	page := rendered.String()
	if strings.Contains(page, appCSSPlaceholder) || strings.Contains(page, i18nJSPlaceholder) || strings.Contains(page, appJSPlaceholder) {
		t.Fatal("rendered index still contains an asset placeholder")
	}
	for _, want := range []string{
		"<style>\n" + trimAssetTerminator(appCSS) + "\n</style>",
		"<script>\n" + trimAssetTerminator(i18nJS) + "\n</script>",
		"<script>\n" + trimAssetTerminator(appJS) + "\n</script>",
	} {
		if !strings.Contains(page, want) {
			t.Fatal("rendered index does not contain an embedded asset")
		}
	}
	if strings.Index(page, trimAssetTerminator(i18nJS)) > strings.Index(page, trimAssetTerminator(appJS)) {
		t.Fatal("i18n script must load before the application script")
	}
}

func TestLoginTemplateInlinesI18nAsset(t *testing.T) {
	var rendered bytes.Buffer
	if err := cleanLoginTemplate.Execute(&rendered, nil); err != nil {
		t.Fatalf("execute login template: %v", err)
	}
	page := rendered.String()
	if strings.Contains(page, i18nJSPlaceholder) {
		t.Fatal("rendered login still contains the i18n asset placeholder")
	}
	if !strings.Contains(page, trimAssetTerminator(i18nJS)) {
		t.Fatal("rendered login does not contain the embedded i18n asset")
	}
}

func TestI18nLocalesHaveMatchingKeys(t *testing.T) {
	parts := strings.SplitN(i18nJS, "\n},\nzh:{\n", 2)
	if len(parts) != 2 {
		t.Fatal("cannot split English and Chinese locale tables")
	}
	keyPattern := regexp.MustCompile(`'([a-zA-Z0-9_.]+)':`)
	keys := func(content string) map[string]bool {
		out := make(map[string]bool)
		for _, match := range keyPattern.FindAllStringSubmatch(content, -1) {
			out[match[1]] = true
		}
		return out
	}
	zhTable := strings.SplitN(parts[1], "\n}};", 2)[0]
	enKeys, zhKeys := keys(parts[0]), keys(zhTable)
	for key := range enKeys {
		if !zhKeys[key] {
			t.Errorf("Chinese locale is missing %q", key)
		}
	}
	for key := range zhKeys {
		if !enKeys[key] {
			t.Errorf("English locale is missing %q", key)
		}
	}
}

func TestRobotJobNamesAlwaysUseChineseCatalog(t *testing.T) {
	if !strings.Contains(appJS, "const table=I18N_MESSAGES.zh||{}") {
		t.Fatal("robot job display is not fixed to the Chinese catalog")
	}
}

func TestDiagnosticsDialogKeepsRawEnglishText(t *testing.T) {
	if !strings.Contains(appJS, "showModal('Diagnostics',body,'<button onclick=\"closeModal()\">Close</button>','diagnostics',false)") {
		t.Fatal("diagnostics dialog is not configured to bypass translation")
	}
	for _, want := range []string{"Start Party Debug", "Stop &amp; Analyze", "partyDebugStatus", "partyDebugActions", "Party Debug Result", "party-debug-result"} {
		if !strings.Contains(appJS, want) {
			t.Fatalf("party debug UI is missing %q", want)
		}
	}
	if strings.Contains(appJS, "partyDebugPanelHTML") || strings.Contains(appCSS, "background:#0f172a") {
		t.Fatal("Diagnostics still contains the old persistent black Party debug panel")
	}
}

func TestSchedulerAlwaysUsesCompactEnglish(t *testing.T) {
	for _, want := range []string{
		`class="scheduler" data-i18n-skip`,
		`<div class="k">Policy</div>`,
		`<div class="k">Attach</div>`,
		"i18nEnglishFormat('scheduler.attach_value'",
		"'{rate}/s · b{batch}'",
		"node.parentElement?.closest('[data-i18n-skip]')",
	} {
		if !strings.Contains(indexHTML+appJS+i18nJS, want) {
			t.Fatalf("scheduler compact English behavior is missing %q", want)
		}
	}
}

func TestRequestedChineseLabelsAndDialogWidths(t *testing.T) {
	for _, want := range []string{
		"'action.market':'拍卖'",
		"'common.cast':'释放'",
		"auto-form",
		"party-account-input",
		".party-account-input{width:124px!important}",
	} {
		if !strings.Contains(appCSS+appJS+i18nJS, want) {
			t.Fatalf("requested web label or width is missing %q", want)
		}
	}
}

func TestMarketDialogUsesCompactAlignedLayout(t *testing.T) {
	for _, want := range []string{
		"dialog.market{width:min(680px,96vw)",
		"dialog.market .formgrid>label{white-space:nowrap}",
		"dialog.market .market-range",
		"showModal('Market',body,foot,'market')",
	} {
		if !strings.Contains(appCSS+appJS, want) {
			t.Fatalf("market dialog is missing compact layout rule %q", want)
		}
	}
}

func TestStoreColumnStaysEnglish(t *testing.T) {
	if strings.Contains(indexHTML, `data-i18n="robots.store"`) {
		t.Fatal("robot Store header still participates in language switching")
	}
	if !strings.Contains(indexHTML, `<th data-i18n-skip>Store</th>`) {
		t.Fatal("robot Store header is not protected from text-node translation")
	}
	if !strings.Contains(appJS, "span.textContent=i18nEnglishFormat('status.'+store)") {
		t.Fatal("robot Store values are not fixed to English")
	}
}

func TestAutoDialogUsesStandardFooterWithoutWrapping(t *testing.T) {
	for _, want := range []string{
		`const foot='<button onclick="submitAuto(null)">Save settings</button>`,
		"showModal('Auto',body,foot)",
		".auto-form>input[type=number]{width:120px}",
		".auto-option{white-space:nowrap}",
		"grid-template-columns:68px 16px 68px max-content",
		`<span>~</span><input id="autoShoutMax"`,
	} {
		if !strings.Contains(appCSS+appJS, want) {
			t.Fatalf("Auto dialog layout is missing %q", want)
		}
	}
}

func TestPortsDialogUsesStandardFooter(t *testing.T) {
	for _, want := range []string{
		`const foot='<button onclick="submitGamePort()">Save Ports</button>`,
		"showModal('Ports',body,foot,'ports')",
		"dialog.ports{width:min(320px,96vw)",
		"dialog.ports .formgrid{grid-template-columns:100px 92px}",
		`const ports=endpoint.ports||{};const body='<div class="formgrid"><label>Game</label>`,
	} {
		if !strings.Contains(appCSS+appJS, want) {
			t.Fatalf("Ports dialog standard footer is missing %q", want)
		}
	}
	for _, removed := range []string{`id="gameHost"`, `id="loginIP"`, `id="auctionHost"`, `id="pointHost"`, `id="relayHost"`, `id="serviceRoot"`, `id="serviceRunScript"`} {
		if strings.Contains(appJS, removed) {
			t.Fatalf("Ports dialog still exposes non-port field %q", removed)
		}
	}
}

func TestMaxOnlineIsCappedAndDocumentsServiceRestart(t *testing.T) {
	for _, want := range []string{
		`id="maxButton"`,
		`id="maxUserNum" type="number" min="1" max="600"`,
		`oninput="clampMaxUserInput(this)"`,
		"function clampMaxUserInput(input){if(Number(input?.value)>600)input.value='600'}",
		"directory.<br>Maximum is 600. Changes take effect after restarting /root/run.",
		"directory.&#10;Maximum is 600. Changes take effect after restarting /root/run.",
		"max_user_num。\\n最大为 600，修改后需重启 /root/run 生效。",
	} {
		if !strings.Contains(indexHTML+appJS+i18nJS, want) {
			t.Fatalf("Max online limit or restart note is missing %q", want)
		}
	}
}

func TestMarketFieldsUseOneCompactAlignment(t *testing.T) {
	for _, want := range []string{
		"dialog.market .formgrid{grid-template-columns:160px minmax(0,1fr)}",
		"grid-template-columns:minmax(0,180px) max-content",
		"grid-template-columns:120px 16px 120px max-content",
		"marketUpgradeMax",
		"marketStackSizes",
		"market-range-price",
		"market-range-short",
		"market-field-price",
		"market-field-short",
		"grid-template-columns:140px 16px 140px max-content",
		"grid-template-columns:72px 16px 72px max-content",
	} {
		if !strings.Contains(appCSS+appJS, want) {
			t.Fatalf("compact market alignment is missing %q", want)
		}
	}
	if strings.Index(appJS, "marketUpgradeMax") > strings.Index(appJS, "marketStackSizes") {
		t.Fatal("stack sizes must appear below upgrade in the market dialog")
	}
}

func TestMarketDialogHasExplicitRebuildWithoutAutoSave(t *testing.T) {
	for _, want := range []string{
		`id="modalSaveButton"`,
		"submitMarketSettings()",
		"marketApplyListingConfig",
		"style==='market'?'':'none'",
	} {
		if !strings.Contains(indexHTML+appJS, want) {
			t.Fatalf("market save behavior is missing %q", want)
		}
	}
	for _, hidden := range []string{"marketCycleSeconds", "marketAutoConcurrent", "marketRestockMaxActions", "marketCollectMaxActions", "marketCollectConcurrent"} {
		if strings.Contains(appJS, hidden) {
			t.Fatalf("market dialog still exposes runtime field %q", hidden)
		}
	}
	if strings.Contains(appJS, "function autoSaveMarketConfig()") {
		t.Fatal("market dialog must not auto-save")
	}
	if strings.Contains(appJS, "Only these rarity digits will be listed; missing rarity is treated as 0</div>") {
		t.Fatal("market rarity field still renders its explanatory note")
	}
}

func TestLoginTemplateEscapesError(t *testing.T) {
	const loginError = `<script>alert("bad")</script>`
	var rendered bytes.Buffer
	if err := cleanLoginTemplate.Execute(&rendered, map[string]string{"Error": loginError}); err != nil {
		t.Fatalf("execute login template: %v", err)
	}
	page := rendered.String()
	if strings.Contains(page, loginError) || !strings.Contains(page, "&lt;script&gt;") {
		t.Fatalf("login error was not HTML-escaped: %q", page)
	}
}
