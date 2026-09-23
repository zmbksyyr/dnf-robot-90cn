package webadmin

import (
	"archive/zip"
	"bytes"
	"net/http"
	"strconv"

	"robot/internal/capability/keypair"
	"robot/internal/shared"
)

func (s *Server) handleKeypairDownload(w http.ResponseWriter, _ *http.Request) {
	if s.nativeBackendOnly(w, shared.CapabilityKeypair) {
		return
	}
	raw, err := callRobot(s.robotAddr, "keypairStatus", nil, robotCallTimeout("keypairStatus"), s.cfg.MaxResponseBytes)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)
		return
	}
	status, ok := parseRobotResult(raw).(map[string]interface{})
	if !ok {
		http.Error(w, "invalid keypair status", http.StatusBadGateway)
		return
	}
	result, ok := status["result"].(map[string]interface{})
	if !ok {
		http.Error(w, "invalid keypair result", http.StatusBadGateway)
		return
	}
	if valid, _ := result["game_valid"].(bool); !valid {
		http.Error(w, "game keypair is not valid", http.StatusConflict)
		return
	}
	defaultPrivate, defaultPublic, err := keypair.DefaultKeypairPEM()
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	if err := addZipFile(zw, "privatekey.pem", defaultPrivate); err != nil {
		_ = zw.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := addZipFile(zw, "publickey.pem", defaultPublic); err != nil {
		_ = zw.Close()
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := zw.Close(); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="tw_game_keypair.zip"`)
	w.Header().Set("Content-Length", strconv.Itoa(buf.Len()))
	_, err = w.Write(buf.Bytes())
	reportResponseWriteError(w, err)
}

func addZipFile(zw *zip.Writer, name string, data []byte) error {
	fw, err := zw.Create(name)
	if err != nil {
		return err
	}
	_, err = fw.Write(data)
	return err
}
