package pvf

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestPatchPVFUpgradeSeparateNoChangeDoesNotCreateBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Script.pvf")
	backupPath := filepath.Join(dir, "config", "state", "backups", "pvf_upgrade_separate", "root", "game", "Script.pvf")
	original := buildUpgradeSeparateTestPVF(t, 7)
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}

	result, err := PatchPVFUpgradeSeparate(path, backupPath, 7)
	if err != nil {
		t.Fatal(err)
	}
	if result.Patched || result.BackupPath != "" || result.Before != 7 || result.After != 7 {
		t.Fatalf("no-op patch result = %+v", result)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatal("no-op patch changed the PVF")
	}
	backups, err := filepath.Glob(backupPath + "*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != 0 {
		t.Fatalf("no-op patch created backups: %v", backups)
	}
}

func TestPatchPVFUpgradeSeparateRequiresRecoveryDestination(t *testing.T) {
	path := filepath.Join(t.TempDir(), "Script.pvf")
	original := buildUpgradeSeparateTestPVF(t, 20)
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := PatchPVFUpgradeSeparate(path, "", 7); err == nil {
		t.Fatal("patch without recovery destination succeeded")
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatal("failed backup requirement changed the PVF")
	}
}

func TestParsePVFPatchEntriesClampsHugeFileCount(t *testing.T) {
	raw := buildUpgradeSeparateTestPVF(t, 7)
	binary.LittleEndian.PutUint32(raw[16:20], 0xFFFFFFFF)
	path := filepath.Join(t.TempDir(), "Script.pvf")
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	// A corrupt file count must not trigger a multi-gigabyte preallocation;
	// the tree is short, so parsing must stay bounded and still work.
	status, err := InspectPVFUpgradeSeparate(path)
	if err != nil {
		t.Fatalf("inspect with corrupt file count: %v", err)
	}
	if status.Value != 7 {
		t.Fatalf("status = %+v, want value 7", status)
	}
	if entries := parsePVFPatchEntries(make([]byte, 40), 0xFFFFFFFF); len(entries) > 2 {
		t.Fatalf("entries = %d, want bounded by tree length", len(entries))
	}
}

func TestPatchPVFUpgradeSeparateRotatesBoundedBackups(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Script.pvf")
	backupPath := filepath.Join(dir, "config", "state", "backups", "pvf_upgrade_separate", "root", "game", "Script.pvf")
	originals := make([][]byte, upgradeSeparateBackupCount+2)
	for i := range originals {
		originals[i] = buildUpgradeSeparateTestPVF(t, 20+i)
		if err := os.WriteFile(path, originals[i], 0640); err != nil {
			t.Fatal(err)
		}
		result, err := PatchPVFUpgradeSeparate(path, backupPath, 7)
		if err != nil {
			t.Fatalf("patch round %d: %v", i, err)
		}
		if !result.Patched || result.Before != 20+i || result.After != 7 || result.BackupPath != backupPath {
			t.Fatalf("patch round %d result = %+v", i, result)
		}
		status, err := InspectPVFUpgradeSeparate(path)
		if err != nil || status.Value != 7 {
			t.Fatalf("inspect round %d status=%+v err=%v", i, status, err)
		}
	}

	backups, err := filepath.Glob(backupPath + "*")
	if err != nil {
		t.Fatal(err)
	}
	if len(backups) != upgradeSeparateBackupCount {
		t.Fatalf("backup count got %d want %d: %v", len(backups), upgradeSeparateBackupCount, backups)
	}
	last := len(originals) - 1
	for index := 1; index <= upgradeSeparateBackupCount; index++ {
		name := backupPath
		if index > 1 {
			name = fmt.Sprintf("%s.%d", backupPath, index-1)
		}
		got, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		want := originals[last-index+1]
		if !bytes.Equal(got, want) {
			t.Fatalf("backup %d does not contain patch round %d input", index, last-index+1)
		}
	}
}

func TestReplaceFileAtomicallyKeepsOriginalWhenCommitFails(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "Script.pvf")
	original := []byte("original")
	if err := os.WriteFile(path, original, 0644); err != nil {
		t.Fatal(err)
	}

	wantErr := errors.New("rename failed")
	err := replaceFileAtomically(path, []byte("replacement"), 0644, func(_, _ string) error {
		return wantErr
	})
	if !errors.Is(err, wantErr) {
		t.Fatalf("replace error got %v want %v", err, wantErr)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, original) {
		t.Fatalf("failed replacement changed original to %q", got)
	}
	temps, err := filepath.Glob(filepath.Join(dir, ".Script.pvf.patch-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 0 {
		t.Fatalf("failed replacement left temp files: %v", temps)
	}
}

func buildUpgradeSeparateTestPVF(t *testing.T, value int) []byte {
	t.Helper()
	label := []byte(upgradeSeparateLabel)
	stringTable := make([]byte, 12+len(label))
	binary.LittleEndian.PutUint32(stringTable[0:4], 1)
	binary.LittleEndian.PutUint32(stringTable[4:8], 8)
	binary.LittleEndian.PutUint32(stringTable[8:12], uint32(8+len(label)))
	copy(stringTable[12:], label)

	upgrade := make([]byte, 12)
	upgrade[2] = 5
	binary.LittleEndian.PutUint32(upgrade[3:7], 0)
	upgrade[7] = 2
	binary.LittleEndian.PutUint32(upgrade[8:12], uint32(value))

	type entry struct {
		name     string
		seed     uint32
		plain    []byte
		offset   uint32
		checksum uint32
		encoded  []byte
	}
	entries := []entry{
		{name: "stringtable.bin", seed: 0x10203040, plain: stringTable},
		{name: upgradeSeparatePath, seed: 0x50607080, plain: upgrade},
	}
	dataSize := 0
	for i := range entries {
		aligned := make([]byte, align4(len(entries[i].plain)))
		copy(aligned, entries[i].plain)
		entries[i].offset = uint32(dataSize)
		entries[i].checksum = pvfDataChecksum(aligned, len(aligned), entries[i].seed)
		entries[i].encoded = make([]byte, len(aligned))
		encryptPVFBlockInto(entries[i].encoded, aligned, entries[i].checksum)
		dataSize += len(aligned)
	}

	tree := make([]byte, 0)
	appendUint32 := func(value uint32) {
		var raw [4]byte
		binary.LittleEndian.PutUint32(raw[:], value)
		tree = append(tree, raw[:]...)
	}
	for _, entry := range entries {
		appendUint32(entry.seed)
		appendUint32(uint32(len(entry.name)))
		tree = append(tree, entry.name...)
		appendUint32(uint32(len(entry.plain)))
		appendUint32(entry.checksum)
		appendUint32(entry.offset)
	}
	tree = append(tree, make([]byte, align4(len(tree))-len(tree))...)
	treeChecksum := pvfDataChecksum(tree, len(tree), uint32(len(entries)))
	encodedTree := make([]byte, len(tree))
	encryptPVFBlockInto(encodedTree, tree, treeChecksum)

	const headerSize = 20
	raw := make([]byte, headerSize+len(encodedTree)+dataSize)
	binary.LittleEndian.PutUint32(raw[0:4], 0)
	binary.LittleEndian.PutUint32(raw[8:12], uint32(len(encodedTree)))
	binary.LittleEndian.PutUint32(raw[12:16], treeChecksum)
	binary.LittleEndian.PutUint32(raw[16:20], uint32(len(entries)))
	copy(raw[headerSize:], encodedTree)
	offset := headerSize + len(encodedTree)
	for _, entry := range entries {
		copy(raw[offset:], entry.encoded)
		offset += len(entry.encoded)
	}
	return raw
}
