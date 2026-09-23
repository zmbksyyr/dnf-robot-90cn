package robottemplate

import (
	"fmt"
	"strings"
	"time"

	robotconfig "robot/internal/capability/robotconfig"
	"robot/internal/foundation/charset"
)

type ShoutTemplates struct {
	Channel  string   `json:"channel"`
	Type     int      `json:"type"`
	Messages []string `json:"messages"`
}

type NamePool struct {
	Label string           `json:"label"`
	Names []string         `json:"names"`
	Grows map[int]NamePool `json:"grows,omitempty"`
}

type NameTemplates struct {
	Common []string         `json:"common"`
	Jobs   map[int]NamePool `json:"jobs"`
}

func CloneShoutTemplates(t ShoutTemplates) ShoutTemplates {
	t.Messages = append([]string(nil), t.Messages...)
	return t
}

func CloneNameTemplates(t NameTemplates) NameTemplates {
	t.Common = append([]string(nil), t.Common...)
	if t.Jobs != nil {
		jobs := make(map[int]NamePool, len(t.Jobs))
		for job, pool := range t.Jobs {
			jobs[job] = cloneNamePool(pool)
		}
		t.Jobs = jobs
	}
	return t
}

func cloneNamePool(pool NamePool) NamePool {
	pool.Names = append([]string(nil), pool.Names...)
	if pool.Grows != nil {
		grows := make(map[int]NamePool, len(pool.Grows))
		for grow, child := range pool.Grows {
			grows[grow] = cloneNamePool(child)
		}
		pool.Grows = grows
	}
	return pool
}

func NameCount(t NameTemplates) int {
	count := len(t.Common)
	for _, job := range t.Jobs {
		count += len(job.Names)
		for _, grow := range job.Grows {
			count += len(grow.Names)
		}
	}
	return count
}

func SafeShoutMessage(msg string) string {
	msg = strings.TrimSpace(msg)
	if msg == "" {
		return "hello"
	}
	// World-shout packets store the message length in one byte. Keep the
	// shared local/world sanitizer within that limit without splitting UTF-8.
	const maxBytes = 255
	var b strings.Builder
	for _, r := range msg {
		if r < 0x20 {
			continue
		}
		next := string(r)
		if b.Len()+len(next) > maxBytes {
			break
		}
		b.WriteString(next)
	}
	out := strings.TrimSpace(b.String())
	if out == "" {
		return "hello"
	}
	return out
}

func PrepareShout(msg string, world bool) (int, string, string) {
	if world {
		return 11, "world", msg
	}
	return 3, "local", msg
}

func AllocateName(uid, job, grow int, used map[string]struct{}, rc robotconfig.RuntimeConfig, tpl NameTemplates, exists func(string) bool, randBetween func(int, int) int) string {
	if used == nil {
		used = make(map[string]struct{})
	}
	if pool, ok := tpl.Jobs[job]; ok {
		if child, ok := pool.Grows[grow]; ok {
			if name, ok := allocateFromPool(child.Names, used, exists, randBetween); ok {
				return name
			}
		}
		if name, ok := allocateFromPool(pool.Names, used, exists, randBetween); ok {
			return name
		}
	}
	if name, ok := allocateFromPool(tpl.Common, used, exists, randBetween); ok {
		return name
	}
	if rc.NameASCIIFallback {
		prefix := rc.NameASCIIPrefix
		if prefix == "" {
			prefix = "twbot"
		}
		for attempt := 0; attempt < 200; attempt++ {
			name := fmt.Sprintf("%s%05d", prefix, (uid+attempt)%100000)
			if reserveName(name, used, exists) {
				return name
			}
		}
	}
	fallbackRunes := []rune("风云星月山海天涯剑影霜雪龙吟夜雨晨光青岚苍穹逐梦无双凌墨羽寒江孤城长歌惊鸿逍遥清欢归舟听潮踏歌流萤锦书朝暮浮生")
	for attempt := 0; attempt < 1000; attempt++ {
		value := uid + attempt
		name := fmt.Sprintf("旅人%c%c%c%c", fallbackRunes[(value/1)%len(fallbackRunes)], fallbackRunes[(value/7)%len(fallbackRunes)], fallbackRunes[(value/31)%len(fallbackRunes)], fallbackRunes[(value/127)%len(fallbackRunes)])
		if reserveName(name, used, exists) {
			return name
		}
	}
	return fmt.Sprintf("旅人%d", time.Now().UnixNano()%1000000)
}

func allocateFromPool(names []string, used map[string]struct{}, exists func(string) bool, randBetween func(int, int) int) (string, bool) {
	if len(names) == 0 {
		return "", false
	}
	start := 0
	if randBetween != nil {
		start = randBetween(0, len(names)-1)
	}
	for offset := range names {
		name := strings.TrimSpace(names[(start+offset)%len(names)])
		if reserveName(name, used, exists) {
			return name, true
		}
	}
	return "", false
}

func reserveName(name string, used map[string]struct{}, exists func(string) bool) bool {
	dbName := DBName(name)
	if _, ok := used[dbName]; ok {
		return false
	}
	if exists != nil && exists(dbName) {
		return false
	}
	used[dbName] = struct{}{}
	return true
}

func DBName(name string) string {
	return NameForEncoding(name, "utf8_cp1252").(string)
}

func FitsGameSlot(name string) bool {
	name = strings.TrimSpace(name)
	if name == "" || len([]byte(name)) > 20 {
		return false
	}
	return string(charset.Windows1252StringBytes(charset.UTF8AsWindows1252String(name))) == name
}

func NameForEncoding(name, encoding string) interface{} {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "utf8_cp1252":
		return charset.UTF8AsWindows1252String(name)
	default:
		return name
	}
}
