package cn90

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
	cn90PVFHeaderSize     = 0x30
	cn90PVFFileItemSize   = 0x18
	cn90PVFGroupItemSize  = 8
	cn90PVFMagic          = 0x69706B6E
	cn90PVFMaxBytes       = 512 * 1024 * 1024
	cn90PVFMaxChunkBytes  = 512 * 1024 * 1024
	cn90PVFMaxStringBytes = 64 * 1024 * 1024
)

type cn90PVFFileItem struct {
	chunk, offset, size, dataType int
}

type cn90PVFGroup struct {
	compressed, original int
}

// cn90PVFArchive is the read-only NPK container used by 90CN. Gameplay field
// projection remains in capability/pvf so other backends can share it.
type cn90PVFArchive struct {
	raw       []byte
	body      int
	files     []cn90PVFFileItem
	groups    []cn90PVFGroup
	paths     map[string]int
	strA      []byte
	strW      []byte
	chunkData map[int][]byte
	protected bool
}

func openCN90PVF(path string) (*cn90PVFArchive, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat 90CN PVF: %w", err)
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("90CN PVF is not a regular file")
	}
	if info.Size() > cn90PVFMaxBytes {
		return nil, fmt.Errorf("90CN PVF exceeds %d bytes", cn90PVFMaxBytes)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read 90CN PVF: %w", err)
	}
	archive := &cn90PVFArchive{raw: raw, paths: make(map[string]int), chunkData: make(map[int][]byte)}
	if err := archive.parse(); err != nil {
		return nil, err
	}
	return archive, nil
}

func (a *cn90PVFArchive) parse() error {
	if len(a.raw) < cn90PVFHeaderSize {
		return fmt.Errorf("90CN PVF header is truncated")
	}
	header := append([]byte(nil), a.raw[:cn90PVFHeaderSize]...)
	for i := 24; i < 28; i++ {
		header[i] ^= 0x55
	}
	cn90PVFDecrypt("HeaD", header, 0x269EC3)
	if binary.LittleEndian.Uint32(header[:4]) != cn90PVFMagic {
		header = append([]byte(nil), a.raw[:cn90PVFHeaderSize]...)
		cn90PVFDecryptProtected("hEAd", header, 0x269EC3)
		a.protected = true
	}
	if binary.LittleEndian.Uint32(header[:4]) != cn90PVFMagic {
		return fmt.Errorf("90CN PVF signature is invalid")
	}
	fileCount := cn90PVFInt(header[24:28])
	bodySize := cn90PVFInt(header[32:36])
	groupCount := cn90PVFInt(header[36:40])
	hashSize := cn90PVFInt(header[40:44])
	nameSize := cn90PVFInt(header[44:48])
	if fileCount < 0 || fileCount > len(a.raw)/cn90PVFFileItemSize || bodySize < 0 || bodySize > len(a.raw) || groupCount < 0 || groupCount > len(a.raw)/cn90PVFGroupItemSize {
		header = append([]byte(nil), a.raw[:cn90PVFHeaderSize]...)
		cn90PVFDecrypt("HeaD", header, 0x269EC3)
		fileCount = cn90PVFInt(header[24:28])
		bodySize = cn90PVFInt(header[32:36])
		groupCount = cn90PVFInt(header[36:40])
		hashSize = cn90PVFInt(header[40:44])
		nameSize = cn90PVFInt(header[44:48])
	}
	if fileCount < 0 || fileCount > len(a.raw)/cn90PVFFileItemSize || bodySize < 0 || bodySize > len(a.raw) || groupCount < 0 || groupCount > len(a.raw)/cn90PVFGroupItemSize {
		header = append([]byte(nil), a.raw[:cn90PVFHeaderSize]...)
		cn90PVFDecryptProtected("hEAd", header, 0x269EC3)
		a.protected = true
		fileCount = cn90PVFInt(header[24:28])
		bodySize = cn90PVFInt(header[32:36])
		groupCount = cn90PVFInt(header[36:40])
		hashSize = cn90PVFInt(header[40:44])
		nameSize = cn90PVFInt(header[44:48])
	}
	for name, value := range map[string]int{"file count": fileCount, "body size": bodySize, "group count": groupCount, "hash size": hashSize, "name size": nameSize} {
		if value < 0 {
			return fmt.Errorf("90CN PVF %s is negative", name)
		}
	}
	tableSize, ok := cn90PVFMultiply(fileCount, cn90PVFFileItemSize)
	if !ok {
		return fmt.Errorf("90CN PVF file table size overflows")
	}
	groupSize, ok := cn90PVFMultiply(groupCount, cn90PVFGroupItemSize)
	if !ok {
		return fmt.Errorf("90CN PVF group table size overflows")
	}
	tableOffset := cn90PVFHeaderSize
	nameOffset := tableOffset + tableSize + hashSize
	groupOffset := nameOffset + nameSize
	a.body = groupOffset + groupSize
	if tableOffset < 0 || nameOffset < tableOffset || groupOffset < nameOffset || a.body < groupOffset || bodySize > len(a.raw)-a.body {
		return fmt.Errorf("90CN PVF sections exceed archive: files=%d body=%d groups=%d hash=%d name=%d body_offset=%d size=%d", fileCount, bodySize, groupCount, hashSize, nameSize, a.body, len(a.raw))
	}
	a.buildStrings(a.raw[nameOffset:groupOffset])
	if err := a.parseGroups(a.raw[groupOffset:a.body], groupCount); err != nil {
		return err
	}
	return a.parseFiles(tableOffset, fileCount)
}

func (a *cn90PVFArchive) buildStrings(data []byte) {
	if len(data) < 16 {
		return
	}
	offset := 8
	if a.protected {
		// The 90CN runtime archive is protected_nkpi: its string pools are
		// keyed StRa/StRw, carry an encoded original size next to the encoded
		// size, and use the protected (UTF-16 seed) keystream.
		a.strA = cn90PVFProtectedStringBuffer(data, &offset, "StRa", 0xAA74472E)
		a.strW = cn90PVFProtectedStringBuffer(data, &offset, "StRw", 0x9A82F037)
		return
	}
	a.strA = cn90PVFStringBuffer(data, &offset, "sTrA", 0xAA74472E)
	a.strW = cn90PVFStringBuffer(data, &offset, "sTrW", 0x9A82F037)
}

func (a *cn90PVFArchive) parseGroups(data []byte, count int) error {
	decoded := append([]byte(nil), data...)
	if a.protected {
		cn90PVFDecryptProtected("grpi", decoded, 0x269EC3)
	} else {
		cn90PVFDecrypt("GRPI", decoded, 0x269EC3)
	}
	a.groups = make([]cn90PVFGroup, 0, count)
	for i := 0; i < count; i++ {
		offset := i * cn90PVFGroupItemSize
		compressed := cn90PVFInt(decoded[offset : offset+4])
		original := cn90PVFInt(decoded[offset+4 : offset+8])
		if compressed < 0 || original < 0 {
			return fmt.Errorf("90CN PVF group %d has a negative size", i)
		}
		a.groups = append(a.groups, cn90PVFGroup{compressed: compressed, original: original})
	}
	return nil
}

func (a *cn90PVFArchive) parseFiles(offset, count int) error {
	a.files = make([]cn90PVFFileItem, 0, count)
	for i := 0; i < count; i++ {
		entryOffset := offset + i*cn90PVFFileItemSize
		if entryOffset < 0 || entryOffset+cn90PVFFileItemSize > len(a.raw) {
			return fmt.Errorf("90CN PVF file table is truncated at entry %d", i)
		}
		entry := a.raw[entryOffset : entryOffset+cn90PVFFileItemSize]
		name := a.resolveString(cn90PVFInt(entry[0:4]))
		dir := a.resolveString(cn90PVFInt(entry[4:8]))
		item := cn90PVFFileItem{
			chunk: cn90PVFInt(entry[8:12]), offset: cn90PVFInt(entry[12:16]),
			size: cn90PVFInt(entry[16:20]), dataType: cn90PVFInt(entry[20:24]),
		}
		a.files = append(a.files, item)
		archivePath := cn90PVFPath(dir + "/" + name)
		if archivePath != "" {
			if _, exists := a.paths[archivePath]; !exists {
				a.paths[archivePath] = i
			}
		}
	}
	return nil
}

func (a *cn90PVFArchive) ReadText(filePath string) (string, error) {
	index, ok := a.paths[cn90PVFPath(filePath)]
	if !ok {
		return "", fmt.Errorf("90CN PVF file not found: %s", filePath)
	}
	item := a.files[index]
	chunk, err := a.chunk(item.chunk)
	if err != nil {
		return "", err
	}
	if item.offset < 0 || item.size < 0 || item.offset > len(chunk)-item.size {
		return "", fmt.Errorf("90CN PVF file %s has an invalid data range", filePath)
	}
	raw := chunk[item.offset : item.offset+item.size]
	switch item.dataType {
	case 1:
		return a.decodeScript(raw), nil
	case 3:
		return cn90PVFUTF16(raw), nil
	default:
		return "", nil
	}
}

func (a *cn90PVFArchive) chunk(index int) ([]byte, error) {
	if data, ok := a.chunkData[index]; ok {
		return data, nil
	}
	if index < 0 || index >= len(a.groups) {
		return nil, fmt.Errorf("90CN PVF chunk %d is out of range", index)
	}
	previous := 0
	if index > 0 {
		previous = a.groups[index-1].compressed
	}
	current := a.groups[index].compressed
	start := a.body + previous
	if current <= previous || start < 0 || current-previous > len(a.raw)-start {
		return nil, fmt.Errorf("90CN PVF chunk %d has an invalid range", index)
	}
	encrypted := append([]byte(nil), a.raw[start:start+current-previous]...)
	if a.protected {
		cn90PVFDecryptProtected("bODy", encrypted, 0x269EC3)
	} else {
		cn90PVFDecrypt("BodY", encrypted, 0x269EC3)
	}
	reader, err := zlib.NewReader(bytes.NewReader(encrypted))
	if err != nil {
		return nil, fmt.Errorf("decompress 90CN PVF chunk %d: %w", index, err)
	}
	want := a.groups[index].original
	if want < 0 || want > cn90PVFMaxChunkBytes {
		_ = reader.Close()
		return nil, fmt.Errorf("90CN PVF chunk %d declares an invalid size %d", index, want)
	}
	// Read at most want+1 bytes: a malicious or corrupt stream must not be
	// able to allocate unbounded memory before the size check below.
	data, readErr := io.ReadAll(io.LimitReader(reader, int64(want)+1))
	closeErr := reader.Close()
	if readErr != nil {
		return nil, fmt.Errorf("read 90CN PVF chunk %d: %w", index, readErr)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("close 90CN PVF chunk %d: %w", index, closeErr)
	}
	if len(data) != want {
		return nil, fmt.Errorf("90CN PVF chunk %d size=%d want=%d", index, len(data), want)
	}
	a.chunkData[index] = data
	return data, nil
}

func (a *cn90PVFArchive) decodeScript(raw []byte) string {
	var out strings.Builder
	out.Grow(len(raw) * 2)
	for offset := 0; offset+5 <= len(raw); offset += 5 {
		value := cn90PVFInt(raw[offset+1 : offset+5])
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

func (a *cn90PVFArchive) resolveString(value int) string {
	if value < 0 {
		return ""
	}
	if value&1 != 0 {
		return cn90PVFUTF16String(a.strW, (value>>1)*2)
	}
	return cn90PVFUTF8String(a.strA, value>>1)
}

// cn90PVFStringBuffer decodes one legacy NKPI string pool (sTrA/sTrW): a
// u32 encoded size, four reserved bytes, the encrypted pool and zlib.
func cn90PVFStringBuffer(data []byte, offset *int, key string, xor uint32) []byte {
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
	cn90PVFDecrypt(key, encrypted, 0x269EC9)
	return cn90PVFInflateStringBuffer(encrypted)
}

// cn90PVFProtectedStringBuffer decodes one protected_nkpi string pool
// (StRa/StRw): a u32 encoded size and a u32 encoded original size, the
// protected-keystream pool and zlib. The original size check is best-effort:
// a mismatch only rejects the pool, it never panics on a corrupt archive.
func cn90PVFProtectedStringBuffer(data []byte, offset *int, key string, xor uint32) []byte {
	if *offset < 0 || *offset+8 > len(data) {
		return nil
	}
	encodedSize := binary.LittleEndian.Uint32(data[*offset : *offset+4])
	encodedOriginalSize := binary.LittleEndian.Uint32(data[*offset+4 : *offset+8])
	*offset += 8
	size := int(encodedSize ^ xor)
	if size <= 0 || size > len(data)-*offset {
		return nil
	}
	encrypted := append([]byte(nil), data[*offset:*offset+size]...)
	*offset += size
	cn90PVFDecryptProtected(key, encrypted, 0x269EC9)
	decoded := cn90PVFInflateStringBuffer(encrypted)
	if decoded == nil {
		return nil
	}
	if want := int(encodedOriginalSize ^ uint32(size)); want >= 0 && want != len(decoded) {
		return nil
	}
	return decoded
}

func cn90PVFInflateStringBuffer(encrypted []byte) []byte {
	reader, err := zlib.NewReader(bytes.NewReader(encrypted))
	if err != nil {
		return nil
	}
	decoded, err := io.ReadAll(io.LimitReader(reader, cn90PVFMaxStringBytes+1))
	_ = reader.Close()
	if err != nil || len(decoded) > cn90PVFMaxStringBytes {
		return nil
	}
	return decoded
}

func cn90PVFDecrypt(key string, data []byte, magic uint32) {
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

func cn90PVFDecryptProtected(key string, data []byte, magic uint32) {
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

func cn90PVFUTF8String(data []byte, start int) string {
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

func cn90PVFUTF16String(data []byte, start int) string {
	if start < 0 || start >= len(data) {
		return ""
	}
	end := start
	for end+1 < len(data) && (data[end] != 0 || data[end+1] != 0) {
		end += 2
	}
	return cn90PVFUTF16(data[start:end])
}

func cn90PVFUTF16(data []byte) string {
	units := make([]uint16, 0, len(data)/2)
	for i := 0; i+1 < len(data); i += 2 {
		units = append(units, binary.LittleEndian.Uint16(data[i:i+2]))
	}
	return string(utf16.Decode(units))
}

func cn90PVFPath(value string) string {
	value = strings.TrimSpace(strings.ReplaceAll(value, "\\", "/"))
	value = strings.TrimPrefix(value, "/")
	value = path.Clean(value)
	if value == "." {
		return ""
	}
	return strings.ToLower(strings.TrimSuffix(value, "/"))
}

func cn90PVFInt(data []byte) int {
	return int(int32(binary.LittleEndian.Uint32(data)))
}

func cn90PVFMultiply(left, right int) (int, bool) {
	maxInt := int(^uint(0) >> 1)
	if left < 0 || right < 0 || (right != 0 && left > maxInt/right) {
		return 0, false
	}
	return left * right, true
}
