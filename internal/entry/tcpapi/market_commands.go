package tcpapi

import (
	"errors"
	"fmt"
	"robot/internal/capability/marketapp"
	"robot/internal/scheduler"
	"robot/internal/shared"
	"strings"
)

var marketApp *marketapp.App

func SetMarketApp(app *marketapp.App) {
	marketApp = app
}

func handleMarketCommand(cmd, pkt string, manager *scheduler.RobotManager) (string, bool) {
	if !isMarketCommand(cmd) {
		return "", false
	}
	if err := manager.RequireCapability(shared.CapabilityMarket); err != nil {
		return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
	}
	switch cmd {
	case "marketStatus":
		app, err := requireMarketApp()
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		return wrapResult(map[string]interface{}{"ok": true, "result": app.Status()}), true
	case "marketKindsProgress":
		app, err := requireMarketApp()
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		var req struct {
			IncludeExpected bool `json:"include_expected,omitempty"`
		}
		if err := decodePayload(pkt, &req); err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := app.AuctionKindsProgress(req.IncludeExpected)
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "marketStart":
		app, err := requireMarketApp()
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := app.SetAutoEnabled(true)
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "marketEnsureServices":
		app, err := requireMarketApp()
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		var req struct {
			Markets []string `json:"markets,omitempty"`
		}
		if err := decodePayload(pkt, &req); err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := app.EnsureServices(req.Markets)
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "marketStop":
		app, err := requireMarketApp()
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := app.SetAutoEnabled(false)
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "marketConfigUpdate":
		app, err := requireMarketApp()
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		var req marketapp.ConfigUpdateRequest
		if err := decodePayload(pkt, &req); err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := app.UpdateConfig(req)
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "marketApplyListingConfig":
		app, err := requireMarketApp()
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		var req marketapp.ConfigUpdateRequest
		if err := decodePayload(pkt, &req); err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := app.StartListingConfigRebuild(req)
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "marketRestockOnce":
		app, err := requireMarketApp()
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		var req marketapp.RestockRequest
		if err := decodePayload(pkt, &req); err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		req.Execute = true
		res, err := runManualRestock(app, req)
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "marketCollectOnce":
		app, err := requireMarketApp()
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		var req marketapp.CollectRequest
		if err := decodePayload(pkt, &req); err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		req.Execute = true
		res, err := runManualCollect(app, req)
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "marketSyncItemInfo":
		app, err := requireMarketApp()
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res := app.SyncItemInfoDAT()
		return wrapResult(map[string]interface{}{"ok": res.Error == "", "error": res.Error, "result": res}), true
	case "marketPVFUpgradeSeparateStatus":
		app, err := requireMarketApp()
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		var req marketapp.PVFUpgradeSeparateRequest
		if err := decodePayload(pkt, &req); err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := app.PVFUpgradeSeparateStatus(req)
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "marketPVFPatchUpgradeSeparate":
		app, err := requireMarketApp()
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		var req marketapp.PVFUpgradeSeparateRequest
		if err := decodePayload(pkt, &req); err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := app.PVFPatchUpgradeSeparate(req)
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "marketClearSystemStock":
		app, err := requireMarketApp()
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := app.ClearSystemMarketStock()
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "marketInstallAuctionGuard":
		app, err := requireMarketApp()
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		var req marketapp.AuctionSearchGuardRequest
		if err := decodePayload(pkt, &req); err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := app.InstallAuctionSearchGuard(req)
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	case "marketPatchAuctionMemory":
		app, err := requireMarketApp()
		if err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		var req marketapp.AuctionMemoryPatchRequest
		if err := decodePayload(pkt, &req); err != nil {
			return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()}), true
		}
		res, err := app.PatchAuctionMemory(req)
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res}), true
	}
	return "", false
}

func isMarketCommand(cmd string) bool {
	switch cmd {
	case "marketStatus", "marketKindsProgress", "marketStart", "marketEnsureServices", "marketStop",
		"marketConfigUpdate", "marketApplyListingConfig", "marketRestockOnce", "marketCollectOnce",
		"marketSyncItemInfo", "marketPVFUpgradeSeparateStatus", "marketPVFPatchUpgradeSeparate",
		"marketClearSystemStock", "marketInstallAuctionGuard", "marketPatchAuctionMemory":
		return true
	default:
		return false
	}
}

func manualMarketTargets(market string) []string {
	if strings.TrimSpace(market) == "" {
		return []string{"auction", "cera"}
	}
	return []string{market}
}

func runManualRestock(app *marketapp.App, req marketapp.RestockRequest) (interface{}, error) {
	targets := manualMarketTargets(req.Market)
	if len(targets) == 1 {
		return app.RestockOnce(req)
	}
	results := make(map[string]marketapp.JobSummary, len(targets))
	var errs []error
	for _, market := range targets {
		marketReq := req
		marketReq.Market = market
		job, err := app.RestockOnce(marketReq)
		results[market] = job
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", market, err))
		}
	}
	return results, errors.Join(errs...)
}

func runManualCollect(app *marketapp.App, req marketapp.CollectRequest) (interface{}, error) {
	targets := manualMarketTargets(req.Market)
	if len(targets) == 1 {
		return app.CollectOnce(req)
	}
	results := make(map[string]marketapp.JobSummary, len(targets))
	var errs []error
	for _, market := range targets {
		marketReq := req
		marketReq.Market = market
		job, err := app.CollectOnce(marketReq)
		results[market] = job
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %w", market, err))
		}
	}
	return results, errors.Join(errs...)
}

func requireMarketApp() (*marketapp.App, error) {
	if marketApp == nil {
		return nil, fmt.Errorf("market app is not initialized")
	}
	return marketApp, nil
}
