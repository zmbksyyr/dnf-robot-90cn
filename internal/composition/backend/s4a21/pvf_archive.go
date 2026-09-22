package s4a21

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"fmt"
	"io"
	"math"
	"os"
	"path"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"golang.org/x/text/encoding/simplifiedchinese"
)

const (
	a21PVFHeaderSize    = 0x30
	a21PVFFileItemSize  = 0x18
	a21PVFGroupItemSize = 8
	a21PVFMagic         = 0x69706B6E
	a21PVFMaxBytes      = 512 * 1024 * 1024
)

type a21PVFFileItem struct {
	chunk, offset, size, dataType int
}

type a21PVFGroup struct {
	compressed, original int
}

// a21PVFArchive is the read-only NPK container used by S4A21. Gameplay field
// projection remains in capability/pvf so other backends can share it.
type a21PVFArchive struct {
	raw       []byte
	body      int
	files     []a21PVFFileItem
	groups    []a21PVFGroup
	paths     map[string]int
	strA      []byte
	strW      []byte
	chunkData map[int][]byte
	protected bool
}

func openA21PVF(path string) (*a21PVFArchive, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat S4A21 PVF: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("S4A21 PVF is not a regular file")
	}
	if info.Size() > a21PVFMaxBytes {
		return nil, fmt.Errorf("S4A21 PVF exceeds %d bytes", a21PVFMaxBytes)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read S4A21 PVF: %w", err)
	}
	archive := &a21PVFArchive{raw: raw, paths: make(map[string]int), chunkData: make(map[int][]byte)}
	if err := archive.parse(); err != nil {
		return nil, err
	}
	return archive, nil
}

func (a *a21PVFArchive) parse() error {
	if len(a.raw) < a21PVFHeaderSize {
		return fmt.Errorf("S4A21 PVF header is truncated")
	}
	header := append([]byte(nil), a.raw[:a21PVFHeaderSize]...)
	for i := 24; i < 28; i++ {
		header[i] ^= 0x55
	}
	a21PVFDecrypt("HeaD", header, 0x269EC3)
	if binary.LittleEndian.Uint32(header[:4]) != a21PVFMagic {
		header = append([]byte(nil), a.raw[:a21PVFHeaderSize]...)
		a21PVFDecryptProtected("hEAd", header, 0x269EC3)
		a.protected = true
	}
	if binary.LittleEndian.Uint32(header[:4]) != a21PVFMagic {
		return fmt.Errorf("S4A21 PVF signature is invalid")
	}
	fileCount := a21PVFInt(header[24:28])
	bodySize := a21PVFInt(header[32:36])
	groupCount := a21PVFInt(header[36:40])
	hashSize := a21PVFInt(header[40:44])
	nameSize := a21PVFInt(header[44:48])
	if fileCount < 0 || fileCount > len(a.raw)/a21PVFFileItemSize || bodySize < 0 || bodySize > len(a.raw) || groupCount < 0 || groupCount > len(a.raw)/a21PVFGroupItemSize {
		header = append([]byte(nil), a.raw[:a21PVFHeaderSize]...)
		a21PVFDecrypt("HeaD", header, 0x269EC3)
		fileCount = a21PVFInt(header[24:28])
		bodySize = a21PVFInt(header[32:36])
		groupCount = a21PVFInt(header[36:40])
		hashSize = a21PVFInt(header[40:44])
		nameSize = a21PVFInt(header[44:48])
	}
	if fileCount < 0 || fileCount > len(a.raw)/a21PVFFileItemSize || bodySize < 0 || bodySize > len(a.raw) || groupCount < 0 || groupCount > len(a.raw)/a21PVFGroupItemSize {
		header = append([]byte(nil), a.raw[:a21PVFHeaderSize]...)
		a21PVFDecryptProtected("hEAd", header, 0x269EC3)
		a.protected = true
		fileCount = a21PVFInt(header[24:28])
		bodySize = a21PVFInt(header[32:36])
		groupCount = a21PVFInt(header[36:40])
		hashSize = a21PVFInt(header[40:44])
		nameSize = a21PVFInt(header[44:48])
	}
	for name, value := range map[string]int{"file count": fileCount, "body size": bodySize, "group count": groupCount, "hash size": hashSize, "name size": nameSize} {
		if value < 0 {
			return fmt.Errorf("S4A21 PVF %s is negative", name)
		}
	}
	tableSize, ok := a21PVFMultiply(fileCount, a21PVFFileItemSize)
	if !ok {
		return fmt.Errorf("S4A21 PVF file table size overflows")
	}
	groupSize, ok := a21PVFMultiply(groupCount, a21PVFGroupItemSize)
	if !ok {
		return fmt.Errorf("S4A21 PVF group table size overflows")
	}
	tableOffset := a21PVFHeaderSize
	nameOffset := tableOffset + tableSize + hashSize
	groupOffset := nameOffset + nameSize
	a.body = groupOffset + groupSize
	if tableOffset < 0 || nameOffset < tableOffset || groupOffset < nameOffset || a.body < groupOffset || bodySize > len(a.raw)-a.body {
		return fmt.Errorf("S4A21 PVF sections exceed archive: files=%d body=%d groups=%d hash=%d name=%d body_offset=%d size=%d", fileCount, bodySize, groupCount, hashSize, nameSize, a.body, len(a.raw))
	}
	a.buildStrings(a.raw[nameOffset:groupOffset])
	if err := a.parseGroups(a.raw[groupOffset:a.body], groupCount); err != nil {
		return err
	}
	return a.parseFiles(tableOffset, fileCount)
}

func (a *a21PVFArchive) buildStrings(data []byte) {
	if len(data) < 16 {
		return
	}
	offset := 8
	a.strA = a21PVFStringBuffer(data, &offset, "sTrA", 0xAA74472E)
	a.strW = a21PVFStringBuffer(data, &offset, "sTrW", 0x9A82F037)
}

func (a *a21PVFArchive) parseGroups(data []byte, count int) error {
	decoded := append([]byte(nil), data...)
	if a.protected {
		a21PVFDecryptProtected("grpi", decoded, 0x269EC3)
	} else {
		a21PVFDecrypt("GRPI", decoded, 0x269EC3)
	}
	a.groups = make([]a21PVFGroup, 0, count)
	for i := 0; i < count; i++ {
		offset := i * a21PVFGroupItemSize
		compressed := a21PVFInt(decoded[offset : offset+4])
		original := a21PVFInt(decoded[offset+4 : offset+8])
		if compressed < 0 || original < 0 {
			return fmt.Errorf("S4A21 PVF group %d has a negative size", i)
		}
		a.groups = append(a.groups, a21PVFGroup{compressed: compressed, original: original})
	}
	return nil
}

func (a *a21PVFArchive) parseFiles(offset, count int) error {
	a.files = make([]a21PVFFileItem, 0, count)
	for i := 0; i < count; i++ {
		entryOffset := offset + i*a21PVFFileItemSize
		if entryOffset < 0 || entryOffset+a21PVFFileItemSize > len(a.raw) {
			return fmt.Errorf("S4A21 PVF file table is truncated at entry %d", i)
		}
		entry := a.raw[entryOffset : entryOffset+a21PVFFileItemSize]
		name := a.resolveString(a21PVFInt(entry[0:4]))
		dir := a.resolveString(a21PVFInt(entry[4:8]))
		item := a21PVFFileItem{
			chunk: a21PVFInt(entry[8:12]), offset: a21PVFInt(entry[12:16]),
			size: a21PVFInt(entry[16:20]), dataType: a21PVFInt(entry[20:24]),
		}
		a.files = append(a.files, item)
		archivePath := a21PVFPath(dir + "/" + name)
		if archivePath != "" {
			if _, exists := a.paths[archivePath]; !exists {
				a.paths[archivePath] = i
			}
		}
	}
	return nil
}

func (a *a21PVFArchive) ReadText(filePath string) (string, error) {
	index, ok := a.paths[a21PVFPath(filePath)]
	if !ok {
		return "", fmt.Errorf("S4A21 PVF file not found: %s", filePath)
	}
	item := a.files[index]
	chunk, err := a.chunk(item.chunk)
	if err != nil {
		return "", err
	}
	if item.offset < 0 || item.size < 0 || item.offset > len(chunk)-item.size {
		return "", fmt.Errorf("S4A21 PVF file %s has an invalid data range", filePath)
	}
	raw := chunk[item.offset : item.offset+item.size]
	switch item.dataType {
	case 1:
		return a.decodeScript(raw), nil
	case 3:
		return a21PVFUTF16(raw), nil
	default:
		return "", nil
	}
}

func (a *a21PVFArchive) chunk(index int) ([]byte, error) {
	if data, ok := a.chunkData[index]; ok {
		return data, nil
	}
	if index < 0 || index >= len(a.groups) {
		return nil, fmt.Errorf("S4A21 PVF chunk %d is out of range", index)
	}
	previous := 0
	if index > 0 {
		previous = a.groups[index-1].compressed
	}
	current := a.groups[index].compressed
	start := a.body + previous
	if current <= previous || start < 0 || current-previous > len(a.raw)-start {
		return nil, fmt.Errorf("S4A21 PVF chunk %d has an invalid range", index)
	}
	encrypted := append([]byte(nil), a.raw[start:start+current-previous]...)
	if a.protected {
		a21PVFDecryptProtected("bODy", encrypted, 0x269EC3)
	} else {
		a21PVFDecrypt("BodY", encrypted, 0x269EC3)
	}
	reader, err := zlib.NewReader(bytes.NewReader(encrypted))
	if err != nil {
		return nil, fmt.Errorf("decompress S4A21 PVF chunk %d: %w", index, err)
	}
	data, readErr := io.ReadAll(reader)
	closeErr := reader.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read S4A21 PVF chunk %d: %w", index, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close S4A21 PVF chunk %d: %w", index, closeErr)
	}
	if want := a.groups[index].original; want != len(data) {
		return nil, fmt.Errorf("S4A21 PVF chunk %d size=%d want=%d", index, len(data), want)
	}
	a.chunkData[index] = data
	return data, nil
}

func (a *a21PVFArchive) decodeScript(raw []byte) string {
	var out strings.Builder
	out.Grow(len(raw) * 2)
	for offset := 0; offset+5 <= len(raw); offset += 5 {
		value := a21PVFInt(raw[offset+1 : offset+5])
		switch raw[offset] {
		case 0:
			fmt.Fprintf(&out, "%d ", value)
		case 2:
			fmt.Fprintf(&out, "%g ", math.Float32frombits(uint32(value)))
		case 3:
			out.WriteByte('\n')
			out.WriteString(a.resolveString(value))
			out.WriteByte('\n')
		case 5:
			out.WriteString("\n{5=``}")
		case 6:
			out.WriteByte('`')
			out.WriteString(a.resolveString(value))
			out.WriteString("` ")
		case 7:
			out.WriteString("\n{7=``}")
		}
	}
	return out.String()
}

func (a *a21PVFArchive) resolveString(value int) string {
	if value < 0 {
		return ""
	}
	if value&1 != 0 {
		return a21PVFUTF16String(a.strW, (value>>1)*2)
	}
	return a21PVFUTF8String(a.strA, value>>1)
}

func a21PVFStringBuffer(data []byte, offset *int, key string, xor uint32) []byte {
	if *offset < 0 || *offset+8 > len(data) {
		return nil
	}
	size := int(binary.LittleEndian.Uint32(data[*offset:*offset+4]) ^ xor)
	*offset += 8
	if size <= 0 || size > len(data)-*offset {
		return nil
	}
	encrypted := append([]byte(nil), data[*offset:*offset+size]...)
	*offset += size
	a21PVFDecrypt(key, encrypted, 0x269EC9)
	reader, err := zlib.NewReader(bytes.NewReader(encrypted))
	if err != nil {
		return nil
	}
	decoded, err := io.ReadAll(reader)
	_ = reader.Close()
	if err != nil {
		return nil
	}
	return decoded
}

func a21PVFDecrypt(key string, data []byte, magic uint32) {
	keyBytes := []byte(key)
	if len(keyBytes) < 4 || len(data) == 0 {
		return
	}
	seed := uint32(0x76826701)*uint32(keyBytes[0]) + 0x1C1*(uint32(keyBytes[3])+0x1C1*(uint32(keyBytes[2])+0x1C1*uint32(keyBytes[1])))
	quadCount := len(data) / 4
	for i := 0; i < quadCount; i++ {
		t1 := 0x343FD*seed + magic
		seed = 0x343FD*t1 + magic
		xorKey := ((seed >> 16) & 0xFFFF) + (t1 & 0xFFFF0000)
		offset := i * 4
		binary.LittleEndian.PutUint32(data[offset:offset+4], binary.LittleEndian.Uint32(data[offset:offset+4])^xorKey)
	}
	if tail := len(data) % 4; tail > 0 {
		t1 := 0x343FD*seed + magic
		t2 := 0x343FD*t1 + magic
		finalKey := (t1 & 0xFFFF0000) + ((t2 >> 16) & 0xFFFF)
		var key [4]byte
		binary.LittleEndian.PutUint32(key[:], finalKey)
		start := len(data) - tail
		for i := 0; i < tail; i++ {
			data[start+i] ^= key[i]
		}
	}
}

func a21PVFDecryptProtected(key string, data []byte, magic uint32) {
	words := utf16.Encode([]rune(key))
	if len(words) < 4 {
		return
	}
	seed := uint32(0x339E9711)*uint32(words[0]) + 0x393*(uint32(words[3])+0x393*(uint32(words[2])+0x393*uint32(words[1])))
	for i := 0; i+4 <= len(data); i += 4 {
		t1 := 0x343FD*seed + magic
		seed = 0x343FD*t1 + magic
		xorKey := (t1 & 0xFFFF0000) + (seed >> 16)
		binary.LittleEndian.PutUint32(data[i:i+4], binary.LittleEndian.Uint32(data[i:i+4])^xorKey)
	}
	if tail := len(data) % 4; tail > 0 {
		t1 := 0x343FD*seed + magic
		t2 := 0x343FD*t1 + magic
		finalKey := (t1 & 0xFFFF0000) + (t2 >> 16)
		var keyBytes [4]byte
		binary.LittleEndian.PutUint32(keyBytes[:], finalKey)
		for i := 0; i < tail; i++ {
			data[len(data)-tail+i] ^= keyBytes[i]
		}
	}
}

func a21PVFUTF8String(data []byte, start int) string {
	if start < 0 || start >= len(data) {
		return ""
	}
	end := bytes.IndexByte(data[start:], 0)
	if end < 0 {
		return ""
	}
	raw := data[start : start+end]
	if utf8.Valid(raw) {
		return string(raw)
	}
	decoded, err := simplifiedchinese.GB18030.NewDecoder().Bytes(raw)
	if err == nil {
		return string(decoded)
	}
	return string(raw)
}

func a21PVFUTF16String(data []byte, start int) string {
	if start < 0 || start >= len(data) {
		return ""
	}
	end := start
	for end+1 < len(data) && (data[end] != 0 || data[end+1] != 0) {
		end += 2
	}
	return a21PVFUTF16(data[start:end])
}

func a21PVFUTF16(data []byte) string {
	units := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		units = append(units, binary.LittleEndian.Uint16(data[i:i+2]))
	}
	return string(utf16.Decode(units))
}

func a21PVFPath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.TrimPrefix(value, "/")
	value = path.Clean(value)
	if value == "." {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(value, "/"))
}

func a21PVFInt(data []byte) int {
	return int(int32(binary.LittleEndian.Uint32(data)))
}

func a21PVFMultiply(left, right int) (int, bool) {
	maxInt := int(^uint(0) >> 1)
	if left < 0 || right < 0 || (right != 0 && left > maxInt/right) {
		return 0, false
	}
	return left * right, true
}
