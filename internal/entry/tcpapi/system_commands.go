package tcpapi

import (
	"bytes"
	"context"
	"runtime"
	"runtime/pprof"

	"robot/internal/scheduler"
)

const maxGoroutineDumpBytes = 1024 * 1024

type cappedProfileBuffer struct {
	bytes.Buffer
	limit     int
	truncated bool
}

func (w *cappedProfileBuffer) Write(p []byte) (int, error) {
	written := len(p)
	remaining := w.limit - w.Len()
	if remaining <= 0 {
		w.truncated = w.truncated || len(p) > 0
		return written, nil
	}
	if len(p) > remaining {
		p = p[:remaining]
		w.truncated = true
	}
	_, _ = w.Buffer.Write(p)
	return written, nil
}

func handleSystemCommand(cmd, pkt string, manager *scheduler.RobotManager) (string, bool) {
	switch cmd {
	case "dashboardStatus":
		return wrapResult(map[string]interface{}{"ok": true, "result": map[string]interface{}{
			"auto": manager.AutoStatus(), "system": manager.SystemStatus(), "scheduler": manager.SchedulerStatus(),
			"database": manager.DatabaseStatus(),
		}}), true
	case "autoStatus":
		return wrapResult(map[string]interface{}{"ok": true, "result": manager.AutoStatus()}), true
	case "schedulerStatus":
		return wrapResult(map[string]interface{}{"ok": true, "result": manager.SchedulerStatus()}), true
	case "operationStatus":
		return wrapResult(map[string]interface{}{"ok": true, "result": manager.OperationStatus()}), true
	case "systemStatus":
		return wrapResult(map[string]interface{}{"ok": true, "result": manager.SystemStatus()}), true
	case "systemAnnouncement":
		return monitorAnnouncement(pkt, manager, scheduler.SystemAnnouncementWebNoticeSingle), true
	case "goroutineDump":
		return wrapResult(map[string]interface{}{"ok": true, "result": goroutineDump()}), true
	case "databaseStatus":
		status := manager.DatabaseStatus()
		return wrapResult(map[string]interface{}{"ok": status.Error == "", "error": status.Error, "result": status}), true
	case "populationReport":
		report, err := manager.PopulationReport(context.Background())
		return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": report}), true
	default:
		return "", false
	}
}

func goroutineDump() map[string]interface{} {
	buf := cappedProfileBuffer{limit: maxGoroutineDumpBytes}
	if prof := pprof.Lookup("goroutine"); prof != nil {
		_ = prof.WriteTo(&buf, 1)
	}
	return map[string]interface{}{
		"count":     runtime.NumGoroutine(),
		"dump":      buf.String(),
		"truncated": buf.truncated,
	}
}

type monitorAnnouncementRequest struct {
	Message string `json:"message"`
}

func monitorAnnouncement(pkt string, manager *scheduler.RobotManager, kind string) string {
	var req monitorAnnouncementRequest
	if err := decodePayload(pkt, &req); err != nil {
		return wrapResult(map[string]interface{}{"ok": false, "error": err.Error()})
	}
	res, err := manager.MonitorAnnouncement(kind, req.Message)
	return wrapResult(map[string]interface{}{"ok": err == nil, "error": errString(err), "result": res})
}
