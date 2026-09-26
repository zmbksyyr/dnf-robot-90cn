package pvf

import (
	"crypto/md5"
	"encoding/hex"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"robot/internal/shared"
)

func deriveItemSetKey(path, body string, item shared.EquipmentCatalogItem) string {
	if item.ItemType <= 0 {
		return ""
	}
	if key := explicitPVFSetKey(body); key != "" {
		return "pvf_" + shortHash(key)
	}
	// Equipment paths are storage layout, not set metadata. In simulator PVFs a
	// shared directory is commonly reused by titles, jewelry, special slots,
	// and unrelated armor pieces. Never expose that path as a set key.
	if partSetIndex := pvfPartSetIndex(body); partSetIndex > 0 {
		return pvfPartSetKey(partSetIndex)
	}
	// Equipment paths and names are presentation/storage data, not set
	// metadata. A missing PVF set field means the item is standalone.
	_ = path
	return ""
}

func pvfPartSetIndex(body string) int { return atoi(pvfTagValue(body, "part set index")) }

func pvfPartSetKey(index int) string { return "pvf_part_" + strconv.Itoa(index) }

type pvfItemSetInfo struct {
	masterID    int
	memberIDs   []int
	descriptors []string
}

func parsePVFItemSetInfo(body string) pvfItemSetInfo {
	return pvfItemSetInfo{
		masterID:    pvfSetMasterID(body),
		memberIDs:   pvfSetItemIDs(body),
		descriptors: pvfSetDescriptors(body),
	}
}

func resolvePVFItemSetKeys(items []shared.EquipmentCatalogItem, setInfo map[int]pvfItemSetInfo) {
	if len(items) == 0 || len(setInfo) == 0 {
		return
	}
	masterFor := make(map[int]int)
	masterDescriptors := make(map[string]int)
	masterIDs := make(map[int]bool)
	for i := range items {
		item := &items[i]
		info := setInfo[item.ID]
		if info.masterID > 0 {
			masterFor[item.ID] = info.masterID
		}
		if !containsInt(info.memberIDs, item.ID) {
			continue
		}
		masterIDs[item.ID] = true
		masterFor[item.ID] = item.ID
		for _, memberID := range info.memberIDs {
			masterFor[memberID] = item.ID
		}
		for _, descriptor := range info.descriptors {
			key := pvfDescriptorLookupKey(*item, descriptor)
			if key == "" {
				continue
			}
			if previous, exists := masterDescriptors[key]; !exists {
				masterDescriptors[key] = item.ID
			} else if previous != item.ID {
				masterDescriptors[key] = 0
			}
		}
	}

	assigned := make(map[int]bool)
	for i := range items {
		item := &items[i]
		if masterID := masterFor[item.ID]; masterID > 0 {
			item.SetKey = pvfMasterSetKey(masterID)
			assigned[item.ID] = true
			continue
		}
		for _, descriptor := range setInfo[item.ID].descriptors {
			masterID := masterDescriptors[pvfDescriptorLookupKey(*item, descriptor)]
			if masterID <= 0 {
				continue
			}
			item.SetKey = pvfMasterSetKey(masterID)
			assigned[item.ID] = true
			break
		}
	}

	attachPVFThemeMatches(items, assigned, masterIDs)
}

func pvfSetMasterID(body string) int {
	return atoi(pvfTagValue(body, "set item master"))
}

func pvfSetItemIDs(body string) []int {
	return pvfSectionInts(body, "set item")
}

func pvfMasterSetKey(masterID int) string {
	return "pvf_" + shortHash("set item master:"+strconv.Itoa(masterID))
}

func pvfSetDescriptors(body string) []string {
	values := []string{pvfTagValue(body, "set name"), pvfTagValue(body, "explain")}
	out := make([]string, 0, len(values))
	seen := make(map[string]bool)
	for _, value := range values {
		value = normalizePVFSetDescriptor(value)
		if value == "" || strings.EqualFold(value, "errorstring") || !pvfSetDescriptorReliable(value) || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}

func pvfSetDescriptorReliable(value string) bool {
	return strings.Contains(value, "套裝") || strings.Contains(value, "套装") || strings.Contains(value, "禮包") || strings.Contains(value, "礼包")
}

func normalizePVFSetDescriptor(value string) string {
	value = strings.ToLower(cleanPVFString(value))
	value = strings.NewReplacer("禮包", "套裝", "礼包", "套裝", "套装", "套裝").Replace(value)
	return strings.Join(strings.Fields(value), "")
}

func pvfDescriptorLookupKey(item shared.EquipmentCatalogItem, descriptor string) string {
	if descriptor == "" {
		return ""
	}
	jobs := append([]int(nil), item.UseJob...)
	sort.Ints(jobs)
	parts := make([]string, 0, len(jobs))
	for _, job := range jobs {
		parts = append(parts, strconv.Itoa(job))
	}
	return strings.Join(parts, ",") + ":" + descriptor
}

type pvfThemeGroup struct {
	key       string
	jobs      string
	minID     int
	maxID     int
	slots     map[int]bool
	tokenHits map[string]int
}

func attachPVFThemeMatches(items []shared.EquipmentCatalogItem, assigned map[int]bool, masterIDs map[int]bool) {
	groupsByKey := make(map[string]*pvfThemeGroup)
	for index := range items {
		item := items[index]
		if !assigned[item.ID] || item.SetKey == "" {
			continue
		}
		group := groupsByKey[item.SetKey]
		if group == nil {
			group = &pvfThemeGroup{key: item.SetKey, jobs: pvfItemJobsKey(item), minID: item.ID, maxID: item.ID, slots: make(map[int]bool), tokenHits: make(map[string]int)}
			groupsByKey[item.SetKey] = group
		}
		if item.ID < group.minID {
			group.minID = item.ID
		}
		if item.ID > group.maxID {
			group.maxID = item.ID
		}
		group.slots[item.ItemType] = true
		for token := range pvfThemeTokens(item.Name2) {
			group.tokenHits[token]++
		}
	}
	groups := make([]*pvfThemeGroup, 0, len(groupsByKey))
	for _, group := range groupsByKey {
		groups = append(groups, group)
	}
	sort.Slice(groups, func(i, j int) bool { return groups[i].key < groups[j].key })

	for i := range items {
		item := &items[i]
		if assigned[item.ID] || item.ItemType < 20 || item.ItemType > 28 || masterIDs[item.ID] {
			continue
		}
		tokens := pvfThemeTokens(item.Name2)
		if len(tokens) == 0 {
			continue
		}
		bestDistance := 0
		matches := make([]*pvfThemeGroup, 0)
		for _, group := range groups {
			if group.jobs != pvfItemJobsKey(*item) || group.slots[item.ItemType] {
				continue
			}
			score := 0
			for token := range tokens {
				if group.tokenHits[token] > score {
					score = group.tokenHits[token]
				}
			}
			if score < 2 {
				continue
			}
			distance := pvfIDRangeDistance(item.ID, group.minID, group.maxID)
			if distance > 10000 {
				continue
			}
			if len(matches) == 0 || distance < bestDistance {
				bestDistance = distance
			}
			matches = append(matches, group)
		}
		if len(matches) == 0 {
			continue
		}
		keys := make([]string, 0, len(matches))
		for _, group := range matches {
			if pvfIDRangeDistance(item.ID, group.minID, group.maxID) <= bestDistance+32 {
				keys = append(keys, group.key)
			}
		}
		if len(keys) > 0 {
			sort.Strings(keys)
			item.SetKey = strings.Join(keys, "|")
			assigned[item.ID] = true
		}
	}
}

func pvfItemJobsKey(item shared.EquipmentCatalogItem) string {
	jobs := append([]int(nil), item.UseJob...)
	sort.Ints(jobs)
	parts := make([]string, 0, len(jobs))
	for _, job := range jobs {
		parts = append(parts, strconv.Itoa(job))
	}
	return strings.Join(parts, ",")
}

func pvfThemeTokens(value string) map[string]bool {
	value = strings.ToLower(value)
	fields := strings.FieldsFunc(value, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	out := make(map[string]bool)
	for _, field := range fields {
		if len(field) < 4 || pvfThemeStopWord(field) || strings.HasPrefix(field, "name") {
			continue
		}
		out[field] = true
	}
	return out
}

func pvfThemeStopWord(value string) bool {
	switch value {
	case "black", "white", "blue", "green", "yellow", "gold", "golden", "silver", "gray", "grey", "brown", "purple", "violet", "pink", "orange", "apricot", "cream", "creamy", "dark", "light", "deep", "red",
		"avatar", "official", "special", "clone", "rare", "event", "package", "equipment", "priest", "gunner", "fighter", "swordman", "slayer", "mage", "thief",
		"coat", "shirt", "shirts", "pants", "shoes", "boots", "belt", "waist", "hair", "face", "skin", "body", "hat", "cap", "neck", "necklace", "earring", "gloves":
		return true
	default:
		return false
	}
}

func pvfIDRangeDistance(id, minID, maxID int) int {
	if id < minID {
		return minID - id
	}
	if id > maxID {
		return id - maxID
	}
	return 0
}

func pvfTagValue(body, tag string) string {
	lines := splitPVFLines(body)
	want := strings.ToLower(strings.TrimSpace(tag))
	for i, line := range lines {
		if strings.ToLower(cleanPVFString(line)) == want {
			return cleanPVFString(nextLine(lines, i))
		}
	}
	return ""
}

func pvfSectionInts(body, tag string) []int {
	lines := splitPVFLines(body)
	want := strings.ToLower(strings.TrimSpace(tag))
	for i, line := range lines {
		if strings.ToLower(cleanPVFString(line)) != want {
			continue
		}
		var out []int
		for _, valueLine := range lines[i+1:] {
			trimmed := strings.TrimSpace(valueLine)
			if strings.HasPrefix(trimmed, "[") {
				break
			}
			for _, field := range strings.Fields(cleanPVFString(trimmed)) {
				if value, err := strconv.Atoi(field); err == nil && value > 0 {
					out = append(out, value)
				}
			}
		}
		return out
	}
	return nil
}

func containsInt(values []int, want int) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func explicitPVFSetKey(body string) string {
	lines := splitPVFLines(body)
	for i, line := range lines {
		lower := strings.ToLower(cleanPVFString(line))
		switch lower {
		case "set item master", "set", "set name", "equipment set":
			if value := cleanPVFString(nextLine(lines, i)); value != "" && !strings.EqualFold(value, "ErrorString") {
				return lower + ":" + value
			}
		}
	}
	return ""
}

func pathSetKey(path string, itemType int) string {
	p := strings.TrimSuffix(normalizePVFPath(path), filepath.Ext(path))
	parts := strings.Split(p, "/")
	if len(parts) <= 2 {
		return ""
	}
	parts = parts[:len(parts)-1]
	out := make([]string, 0, len(parts))
	for _, part := range parts {
		if part == "" || itemTypePathSegment(part) {
			continue
		}
		out = append(out, part)
	}
	if len(out) < 2 {
		return ""
	}
	return strings.Join(out, "/")
}

func nameSetKey(body string, itemType int) string {
	lines := splitPVFLines(body)
	for i, line := range lines {
		if line != "[name]" {
			continue
		}
		name := cleanPVFString(nextLine(lines, i))
		if name == "" || strings.EqualFold(name, "ErrorString") {
			return ""
		}
		name = strings.ToLower(name)
		for _, word := range itemTypeWords(itemType) {
			name = strings.ReplaceAll(name, word, "")
		}
		name = strings.Join(strings.Fields(name), "")
		if len([]rune(name)) < 2 {
			return ""
		}
		return name
	}
	return ""
}

// Some simulator PVFs do not expose avatar set-master records. Their item names
// still carry a shared theme while color and slot words differ per slot.
func attachAvatarNameFamilies(items []shared.EquipmentCatalogItem) {
	type familyGroup struct {
		key   string
		slots map[int]bool
	}
	groups := make(map[string]*familyGroup)
	for index := range items {
		item := items[index]
		if item.ItemType < 20 || item.ItemType > 29 {
			continue
		}
		family := avatarNameFamily(item.Name)
		if family == "" {
			continue
		}
		key := pvfItemJobsKey(item) + ":" + family
		group := groups[key]
		if group == nil {
			group = &familyGroup{key: "avatar_" + shortHash(key), slots: make(map[int]bool)}
			groups[key] = group
		}
		group.slots[item.ItemType] = true
	}
	for index := range items {
		item := items[index]
		if item.ItemType < 20 || item.ItemType > 29 || strings.HasPrefix(item.SetKey, "pvf_") {
			continue
		}
		family := avatarNameFamily(item.Name)
		if family == "" {
			continue
		}
		group := groups[pvfItemJobsKey(item)+":"+family]
		if group == nil || len(group.slots) < 6 {
			continue
		}
		items[index].SetKey = group.key
	}
}

func attachEquipmentNameFamilies(items []shared.EquipmentCatalogItem) {
	type familyGroup struct {
		key   string
		slots map[int]bool
	}
	groups := make(map[string]*familyGroup)
	for _, item := range items {
		if !equipmentArmorSetSlot(item.ItemType) {
			continue
		}
		family := equipmentNameFamily(item.Name)
		if family == "" {
			continue
		}
		key := pvfItemJobsKey(item) + ":" + family
		group := groups[key]
		if group == nil {
			group = &familyGroup{key: "equipment_" + shortHash(key), slots: make(map[int]bool)}
			groups[key] = group
		}
		group.slots[item.ItemType] = true
	}
	for index := range items {
		item := items[index]
		if !equipmentArmorSetSlot(item.ItemType) || strings.HasPrefix(item.SetKey, "pvf_") {
			continue
		}
		family := equipmentNameFamily(item.Name)
		group := groups[pvfItemJobsKey(item)+":"+family]
		if group == nil || len(group.slots) < 5 {
			continue
		}
		items[index].SetKey = group.key
	}
}

// NormalizeEquipmentSetKeys repairs catalogs produced by older Robot builds.
// Only explicit PVF set keys are retained. Paths and display names cannot
// prove a set relationship and are deliberately discarded.
func NormalizeEquipmentSetKeys(items []shared.EquipmentCatalogItem) {
	for i := range items {
		if items[i].ItemType >= 1 && items[i].ItemType <= 12 && !strings.HasPrefix(items[i].SetKey, "pvf_") {
			items[i].SetKey = ""
		}
	}
	attachEquipmentNameFamilies(items)
}

func equipmentArmorSetSlot(itemType int) bool {
	// A five-piece set is the armor block only. Weapons, jewelry and the two
	// special slots use independent item families and must not inflate armor
	// set coverage.
	return itemType >= 3 && itemType <= 7
}

func equipmentNameFamily(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || strings.EqualFold(value, "errorstring") {
		return ""
	}
	for _, word := range []string{
		"黑色", "白色", "红色", "绿色", "蓝色", "黄色", "金色", "银色", "灰色", "紫色", "粉色", "橙色", "杏色", "乳白色", "古铜色", "巧克力色",
		"武器", "称号", "上衣", "护肩", "肩甲", "下装", "裤子", "鞋", "鞋子", "战靴", "长靴", "靴", "腰带", "腰部", "项链", "手镯", "戒指", "辅助装备", "魔法石", "胸甲", "护腿", "头盔",
	} {
		value = strings.ReplaceAll(value, word, "")
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, value)
	if len([]rune(value)) < 3 || strings.HasPrefix(value, "name") {
		return ""
	}
	return value
}

func avatarNameFamily(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" || strings.EqualFold(value, "errorstring") {
		return ""
	}
	for _, word := range []string{
		"黑色", "白色", "红色", "绿色", "蓝色", "黄色", "金色", "银色", "灰色", "紫色", "粉色", "橙色", "杏色", "乳白色", "古铜色", "巧克力色", "透明",
		"高级", "稀有", "普通", "华丽", "灿烂", "白金", "帽子", "头发", "脸部", "面部", "眼影", "墨镜", "耳环", "胸部", "胸", "上衣", "外套", "裤子", "鞋子", "鞋", "腰部", "腰带", "皮肤", "光环", "装扮", "套装",
		"cap", "hat", "hair", "face", "neck", "coat", "jacket", "pants", "shoes", "waist", "skin", "aura", "avatar",
	} {
		value = strings.ReplaceAll(value, word, "")
	}
	value = strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return r
		}
		return -1
	}, value)
	if len([]rune(value)) < 3 || strings.HasPrefix(value, "name") {
		return ""
	}
	return value
}

func itemTypePathSegment(part string) bool {
	switch strings.ToLower(part) {
	case "weapon", "titlename", "title", "coat", "shoulder", "pants", "shoes", "waist", "belt",
		"amulet", "necklace", "wrist", "bracelet", "ring", "support", "magicstone", "magic stone", "magic_stone",
		"cap", "hat", "hair", "face", "neck", "skin", "aura",
		"artifact", "redartifact", "blueartifact", "greenartifact", "red_artifact", "blue_artifact", "green_artifact":
		return true
	default:
		return false
	}
}

func itemTypeWords(itemType int) []string {
	switch itemType {
	case 1:
		return []string{"weapon"}
	case 2:
		return []string{"title", "titlename", "title name"}
	case 3, 23:
		return []string{"coat"}
	case 4:
		return []string{"shoulder"}
	case 5, 24:
		return []string{"pants"}
	case 6, 25:
		return []string{"shoes"}
	case 7, 27:
		return []string{"waist", "belt"}
	case 8:
		return []string{"amulet", "necklace"}
	case 9:
		return []string{"wrist", "bracelet"}
	case 10:
		return []string{"ring"}
	case 11:
		return []string{"support"}
	case 12:
		return []string{"magicstone", "magic stone", "magic_stone"}
	case 20:
		return []string{"cap", "hat"}
	case 21:
		return []string{"hair"}
	case 22:
		return []string{"face"}
	case 26:
		return []string{"neck", "breast"}
	case 28:
		return []string{"skin"}
	case 29:
		return []string{"aura"}
	case 31:
		return []string{"artifact red", "redartifact", "red_artifact"}
	case 32:
		return []string{"artifact blue", "blueartifact", "blue_artifact"}
	case 33:
		return []string{"artifact green", "greenartifact", "green_artifact"}
	default:
		return nil
	}
}

func shortHash(value string) string {
	sum := md5.Sum([]byte(strings.ToLower(strings.TrimSpace(value))))
	return hex.EncodeToString(sum[:])[:10]
}

func jobFromEquipmentPath(path string) int {
	p := "/" + strings.ToLower(normalizePVFPath(path)) + "/"
	switch {
	case strings.Contains(p, "/character/swordman/at_avatar/"):
		return 11
	case strings.Contains(p, "/character/swordman/"):
		return 0
	case strings.Contains(p, "/character/fighter/at_avatar/"):
		return 7
	case strings.Contains(p, "/character/fighter/"):
		return 1
	case strings.Contains(p, "/character/gunner/at_avatar/"):
		return 5
	case strings.Contains(p, "/character/gunner/"):
		return 2
	case strings.Contains(p, "/character/mage/at_avatar/"):
		return 8
	case strings.Contains(p, "/character/mage/"):
		return 3
	case strings.Contains(p, "/character/priest/at_avatar/"):
		return 14
	case strings.Contains(p, "/character/priest/"):
		return 4
	case strings.Contains(p, "/character/thief/"):
		return 6
	case strings.Contains(p, "/character/demonicswordman/"):
		return 9
	default:
		return -1
	}
}

func applyEquipmentPathJob(item *shared.EquipmentCatalogItem, pathJob int) {
	if item == nil || pathJob < 0 {
		return
	}
	if item.ItemType >= 20 && item.ItemType <= 29 {
		item.UseJob = []int{pathJob}
		return
	}
	if len(item.UseJob) == 0 {
		item.UseJob = []int{pathJob}
	}
}

func appendUniqueInt(values []int, value int) []int {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func equipmentTypeFromPath(path string) (int, string) {
	p := "/" + strings.ToLower(normalizePVFPath(path)) + "/"
	if !strings.Contains(p, "/avatar/") && !strings.Contains(p, "/at_avatar/") {
		return 0, ""
	}
	switch {
	case strings.Contains(p, "/cap/"):
		return 20, "hatavatar"
	case strings.Contains(p, "/hair/"):
		return 21, "hairavatar"
	case strings.Contains(p, "/face/"):
		return 22, "faceavatar"
	case strings.Contains(p, "/coat/"):
		return 23, "coatavatar"
	case strings.Contains(p, "/pants/"):
		return 24, "pantsavatar"
	case strings.Contains(p, "/shoes/"):
		return 25, "shoesavatar"
	case strings.Contains(p, "/neck/"):
		return 26, "breastavatar"
	case strings.Contains(p, "/belt/"):
		return 27, "waistavatar"
	case strings.Contains(p, "/skin/"):
		return 28, "skinavatar"
	case strings.Contains(p, "/aura/"):
		return 29, "auroraavatar"
	default:
		return 0, ""
	}
}

type pvfListEntry struct {
	ID   int
	Path string
}

func parsePVFList(text string) []pvfListEntry {
	rawFields := strings.Fields(strings.ReplaceAll(text, "`", ""))
	fields := make([]string, 0, len(rawFields))
	for _, field := range rawFields {
		if strings.EqualFold(field, "#PVF_File") {
			continue
		}
		fields = append(fields, field)
	}
	var out []pvfListEntry
	for i := 0; i+1 < len(fields); i += 2 {
		id := atoi(fields[i])
		path := normalizePVFPath(fields[i+1])
		if id > 0 && path != "" {
			out = append(out, pvfListEntry{ID: id, Path: path})
		}
	}
	return out
}

type townArea struct {
	ID      int
	MapPath string
	Gate    bool
	Kind    string
}

func parseTownAreas(body string) []townArea {
	var out []townArea
	start := 0
	for {
		i := strings.Index(body[start:], "[area]")
		if i < 0 {
			break
		}
		i += start
		j := strings.Index(body[i:], "[/area]")
		if j < 0 {
			break
		}
		j += i
		block := body[i+len("[area]") : j]
		fields := strings.Fields(strings.ReplaceAll(block, "`", ""))
		if len(fields) >= 2 {
			id := atoi(fields[0])
			mapPath := normalizePVFPath(fields[1])
			if id >= 0 && mapPath != "" {
				lowerBlock := strings.ToLower(block)
				kind := "other"
				switch {
				case strings.Contains(lowerBlock, "[pvp]"):
					kind = "pvp"
				case strings.Contains(lowerBlock, "[gate]"):
					kind = "gate"
				case strings.Contains(lowerBlock, "[normal]"):
					kind = "normal"
				}
				out = append(out, townArea{ID: id, MapPath: mapPath, Gate: kind == "gate", Kind: kind})
			}
		}
		start = j + len("[/area]")
	}
	return out
}

func townMapArchivePath(path string) string {
	path = normalizePVFPath(path)
	if path == "" || strings.HasPrefix(path, "map/") {
		return path
	}
	return "map/" + path
}

func townMapMovableBounds(body string) (xMin, xMax, yMin, yMax int, ok bool) {
	rectangles := townMapMovableRectangles(body)
	if len(rectangles) == 0 {
		return 0, 0, 0, 0, false
	}
	xMin, xMax = rectangles[0].XMin, rectangles[0].XMax
	yMin, yMax = rectangles[0].YMin, rectangles[0].YMax
	for _, rectangle := range rectangles[1:] {
		if rectangle.XMin < xMin {
			xMin = rectangle.XMin
		}
		if rectangle.XMax > xMax {
			xMax = rectangle.XMax
		}
		if rectangle.YMin < yMin {
			yMin = rectangle.YMin
		}
		if rectangle.YMax > yMax {
			yMax = rectangle.YMax
		}
	}
	return xMin, xMax, yMin, yMax, true
}

func townMapMovableRectangles(body string) []shared.MapRectangle {
	values := pvfDirectTagInts(body, "virtual movable area")
	if len(values)%4 != 0 {
		return nil
	}
	rectangles := make([]shared.MapRectangle, 0, len(values)/4)
	for i := 0; i+3 < len(values); i += 4 {
		x, y, width, height := values[i], values[i+1], values[i+2], values[i+3]
		if width <= 0 || height <= 0 || width > 10000 || height > 10000 {
			continue
		}
		right, bottom := x+width, y+height
		if right < 0 || bottom < 0 || x > 65535 || y > 65535 {
			continue
		}
		if x < 0 {
			x = 0
		}
		if y < 0 {
			y = 0
		}
		if right > 65535 {
			right = 65535
		}
		if bottom > 65535 {
			bottom = 65535
		}
		rectangles = append(rectangles, shared.MapRectangle{XMin: x, XMax: right, YMin: y, YMax: bottom})
	}
	return rectangles
}

func pvfDirectTagInts(body, tag string) []int {
	lines := splitPVFLines(body)
	want := strings.ToLower(strings.TrimSpace(tag))
	inside := false
	depth := 0
	var out []int
	for _, line := range lines {
		trimmed := strings.Trim(strings.TrimSpace(line), "`")
		name, closing, isTag := pvfTagLine(trimmed)
		if !inside {
			if isTag && !closing && name == want {
				inside = true
			}
			continue
		}
		if isTag {
			if closing && depth == 0 && name == want {
				return out
			}
			if closing {
				if depth > 0 {
					depth--
				}
			} else {
				depth++
			}
			continue
		}
		if depth > 0 {
			continue
		}
		for _, field := range strings.Fields(trimmed) {
			value, err := strconv.Atoi(field)
			if err == nil {
				out = append(out, value)
			}
		}
	}
	return out
}

func pvfTagLine(line string) (name string, closing, ok bool) {
	line = strings.TrimSpace(line)
	if len(line) < 2 || (!strings.HasPrefix(line, "[") && !strings.HasSuffix(line, "]")) {
		return "", false, false
	}
	name = strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(line, "["), "]"))
	if strings.HasPrefix(name, "/") {
		closing = true
		name = strings.TrimSpace(strings.TrimPrefix(name, "/"))
	}
	if name == "" {
		return "", false, false
	}
	return strings.ToLower(name), closing, true
}

func pvfTagInts(body, tag string) []int {
	lines := splitPVFLines(body)
	want := strings.ToLower(strings.TrimSpace(tag))
	for i, line := range lines {
		if strings.ToLower(cleanPVFString(line)) != want {
			continue
		}
		var out []int
		for _, valueLine := range lines[i+1:] {
			trimmed := strings.Trim(strings.TrimSpace(valueLine), "`")
			if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
				break
			}
			for _, field := range strings.Fields(trimmed) {
				value, err := strconv.Atoi(field)
				if err == nil {
					out = append(out, value)
				}
			}
		}
		return out
	}
	return nil
}

func parseJobs(text string) []int {
	seen := map[int]bool{}
	var out []int
	add := func(name string) {
		id := jobID(cleanPVFString(name))
		if id >= 0 && !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	rest := text
	for {
		start := strings.Index(rest, "[")
		if start < 0 {
			break
		}
		end := strings.Index(rest[start+1:], "]")
		if end < 0 {
			break
		}
		add(rest[start+1 : start+1+end])
		rest = rest[start+1+end+1:]
	}
	if len(out) > 0 {
		return out
	}
	fields := strings.Fields(cleanPVFString(text))
	for i := 0; i < len(fields); i++ {
		field := fields[i]
		if i+1 < len(fields) {
			pair := field + " " + fields[i+1]
			before := len(out)
			add(pair)
			if len(out) > before {
				i++
				continue
			}
		}
		add(field)
	}
	return out
}

func parseStackableTradeBlock(body string, item *shared.EquipmentCatalogItem) {
	if item == nil {
		return
	}
	block := section(body, "[trade]", "[/trade]")
	if strings.TrimSpace(block) == "" {
		return
	}
	item.TradeBlock = true
	if value, ok := parsePVFBoolFlag(block, "available trade"); ok {
		item.CanTrade = &value
		if value {
			item.Trade = true
		} else {
			item.NoTrade = true
		}
	}
	if value, ok := parsePVFBoolFlag(block, "available auction"); ok {
		item.CanAuction = &value
		item.Auction = value
	}
	if value, ok := parsePVFBoolFlag(block, "available shop"); ok {
		item.CanShop = &value
		item.Shop = value
	}
	if value, ok := parsePVFBoolFlag(block, "available drop"); ok {
		item.CanDrop = &value
	}
}

func parsePVFBoolFlag(block, name string) (bool, bool) {
	lines := splitPVFLines(block)
	name = strings.ToLower(strings.TrimSpace(name))
	for i, line := range lines {
		cleaned := strings.ToLower(cleanPVFString(line))
		if !strings.Contains(cleaned, name) {
			continue
		}
		fields := strings.Fields(cleaned)
		for idx, field := range fields {
			if field == "0" {
				return false, true
			}
			if field == "1" {
				return true, true
			}
			if strings.Contains(field, name) && idx+1 < len(fields) {
				return atoi(fields[idx+1]) != 0, true
			}
		}
		if i+1 < len(lines) {
			return atoi(lines[i+1]) != 0, true
		}
		return false, true
	}
	return false, false
}

func jobID(job string) int {
	switch strings.ToLower(strings.ReplaceAll(job, " ", "")) {
	case "all":
		return 100
	case "swordman":
		return 0
	case "fighter":
		return 1
	case "gunner":
		return 2
	case "mage":
		return 3
	case "priest":
		return 4
	case "atpriest":
		return 14
	case "atgunner":
		return 5
	case "thief":
		return 6
	case "atfighter":
		return 7
	case "atmage":
		return 8
	case "demonicswordman":
		return 9
	case "creatormage":
		return 10
	default:
		return -1
	}
}

func equipmentType(v string) int {
	switch normalizeEquipmentTypeName(v) {
	case "weapon":
		return 1
	case "titlename", "title", "title name":
		return 2
	case "coat":
		return 3
	case "shoulder":
		return 4
	case "pants":
		return 5
	case "shoes":
		return 6
	case "waist":
		return 7
	case "amulet":
		return 8
	case "wrist":
		return 9
	case "ring":
		return 10
	case "support":
		return 11
	case "magicstone", "magic stone", "magic_stone":
		return 12
	case "hatavatar":
		return 20
	case "hairavatar":
		return 21
	case "faceavatar":
		return 22
	case "coatavatar":
		return 23
	case "pantsavatar":
		return 24
	case "shoesavatar":
		return 25
	case "breastavatar":
		return 26
	case "waistavatar":
		return 27
	case "skinavatar":
		return 28
	case "auroraavatar":
		return 29
	case "creature":
		return 30
	case "artifactred":
		return 31
	case "artifactblue":
		return 32
	case "artifactgreen":
		return 33
	default:
		return -1
	}
}

func normalizeEquipmentTypeName(v string) string {
	// Some adapter PVFs append a numeric subtype after the bracketed type,
	// for example "[weapon] 20". The common catalog only needs the type token.
	v = strings.ToLower(strings.TrimSpace(v))
	if start := strings.IndexByte(v, '['); start >= 0 {
		if end := strings.IndexByte(v[start+1:], ']'); end > 0 {
			v = v[start+1 : start+1+end]
		}
	} else if end := strings.IndexByte(v, ']'); end > 0 {
		// cleanPVFString may already have removed the opening delimiters.
		v = v[:end]
	} else {
		v = cleanPVFString(v)
	}
	v = cleanPVFString(v)
	v = strings.NewReplacer(" ", "", "_", "", "-", "").Replace(v)
	switch v {
	case "redartifact", "creatureartifactred", "creatureredartifact", "petartifactred", "petredartifact":
		return "artifactred"
	case "blueartifact", "creatureartifactblue", "creatureblueartifact", "petartifactblue", "petblueartifact":
		return "artifactblue"
	case "greenartifact", "creatureartifactgreen", "creaturegreenartifact", "petartifactgreen", "petgreenartifact":
		return "artifactgreen"
	default:
		return v
	}
}

func section(text, startTag, endTag string) string {
	start := strings.Index(text, startTag)
	if start < 0 {
		return ""
	}
	start += len(startTag)
	end := strings.Index(text[start:], endTag)
	if end < 0 {
		return ""
	}
	return text[start : start+end]
}

func splitPVFLines(text string) []string {
	text = strings.ReplaceAll(text, "\r\n", "\n")
	text = strings.ReplaceAll(text, "\r", "\n")
	text = strings.ReplaceAll(text, "\t", "\n")
	raw := strings.Split(text, "\n")
	out := make([]string, 0, len(raw))
	for _, line := range raw {
		line = strings.TrimSpace(line)
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

func nextLine(lines []string, i int) string {
	if i+1 >= len(lines) {
		return ""
	}
	return lines[i+1]
}

func cleanPVFString(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Trim(s, "`[]\x00")
	return strings.TrimSpace(s)
}

func cleanPVFTableString(s string) string {
	s = strings.TrimSpace(strings.TrimRight(s, "\x00"))
	return strings.TrimSpace(s)
}

func normalizePVFPath(s string) string {
	s = strings.TrimSpace(strings.Trim(s, "`\x00"))
	s = strings.ReplaceAll(s, "\\", "/")
	return strings.ToLower(s)
}

func atoi(s string) int {
	s = cleanPVFString(s)
	n, _ := strconv.Atoi(s)
	return n
}
