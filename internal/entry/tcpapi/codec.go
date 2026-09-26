package tcpapi

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"

	foundationconfig "robot/internal/foundation/config"
)

var commandPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

// parseRequestPacket validates a request frame and returns the parsed fields.
// Callers must route and decode exclusively from the returned fields: the
// parsed view is the authoritative one, unlike a raw substring search.
func parseRequestPacket(pkt string) (map[string]string, error) {
	fields, err := parseXMLFrame(pkt, map[string]bool{"c": true, "json": true, "key": true})
	if err != nil {
		return nil, err
	}
	cmd := strings.TrimSpace(fields["c"])
	if !commandPattern.MatchString(cmd) {
		return nil, fmt.Errorf("invalid command")
	}
	if _, hasJSON := fields["json"]; hasJSON {
		if _, hasKey := fields["key"]; hasKey {
			return nil, fmt.Errorf("request must not contain both json and key payloads")
		}
	}
	return fields, nil
}

func parseXMLFrame(raw string, allowed map[string]bool) (map[string]string, error) {
	decoder := xml.NewDecoder(strings.NewReader(raw))
	decoder.Strict = true
	first, err := decoder.Token()
	if err != nil {
		return nil, fmt.Errorf("invalid frame: %w", err)
	}
	start, ok := first.(xml.StartElement)
	if !ok || start.Name.Space != "" || start.Name.Local != "tw" || len(start.Attr) != 0 {
		return nil, fmt.Errorf("frame root must be <tw>")
	}
	fields := make(map[string]string)
	for {
		token, err := decoder.Token()
		if err != nil {
			return nil, fmt.Errorf("incomplete frame: %w", err)
		}
		switch value := token.(type) {
		case xml.CharData:
			if strings.TrimSpace(string(value)) != "" {
				return nil, fmt.Errorf("unexpected text outside frame fields")
			}
		case xml.StartElement:
			name := value.Name.Local
			if value.Name.Space != "" || !allowed[name] || len(value.Attr) != 0 {
				return nil, fmt.Errorf("unexpected frame field %q", name)
			}
			if _, duplicate := fields[name]; duplicate {
				return nil, fmt.Errorf("duplicate frame field %q", name)
			}
			text, err := decodeFlatElement(decoder, value)
			if err != nil {
				return nil, err
			}
			fields[name] = text
		case xml.Comment, xml.ProcInst, xml.Directive:
			// Comments and processing instructions are not part of the
			// protocol. Rejecting them keeps the validated view identical to
			// the executed view; silently skipping them once allowed a
			// <c>cleanup</c> inside a comment to bypass command validation.
			return nil, fmt.Errorf("unexpected frame token %T", value)
		case xml.EndElement:
			if value.Name != start.Name {
				return nil, fmt.Errorf("unexpected closing tag %q", value.Name.Local)
			}
			if token, err := decoder.Token(); err != io.EOF {
				if err == nil {
					return nil, fmt.Errorf("trailing data after frame: %T", token)
				}
				return nil, fmt.Errorf("trailing data after frame: %w", err)
			}
			return fields, nil
		}
	}
}

func decodeFlatElement(decoder *xml.Decoder, start xml.StartElement) (string, error) {
	var text strings.Builder
	for {
		token, err := decoder.Token()
		if err != nil {
			return "", fmt.Errorf("incomplete field %q: %w", start.Name.Local, err)
		}
		switch value := token.(type) {
		case xml.CharData:
			text.Write([]byte(value))
		case xml.StartElement:
			return "", fmt.Errorf("nested field %q is not allowed", value.Name.Local)
		case xml.EndElement:
			if value.Name != start.Name {
				return "", fmt.Errorf("unexpected closing tag %q", value.Name.Local)
			}
			return text.String(), nil
		default:
			return "", fmt.Errorf("unexpected frame token %T in field %q", value, start.Name.Local)
		}
	}
}

func decodePayload(pkt string, dst interface{}) error {
	fields, err := parseXMLFrame(pkt, map[string]bool{"c": true, "json": true, "key": true})
	if err != nil {
		return fmt.Errorf("invalid json payload: %w", err)
	}
	payload := strings.TrimSpace(fields["json"])
	if payload == "" {
		payload = strings.TrimSpace(fields["key"])
	}
	if payload == "" {
		payload = "{}"
	}
	if err := foundationconfig.DecodeJSONBytes([]byte(payload), dst); err != nil {
		return fmt.Errorf("invalid json payload: %w", err)
	}
	return nil
}

func wrapResult(v interface{}) string {
	data, err := json.Marshal(v)
	if err != nil {
		data, _ = json.Marshal(map[string]interface{}{"ok": false, "error": "encode response: " + err.Error()})
	}
	return "<tw><result>" + string(data) + "</result></tw>"
}

func errString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
