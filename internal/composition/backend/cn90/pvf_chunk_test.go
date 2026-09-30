package cn90

import (
	"bytes"
	"compress/zlib"
	"testing"
)

func TestChunkRejectsStreamLargerThanDeclaredSize(t *testing.T) {
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write(make([]byte, 1024*1024)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	encrypted := append([]byte(nil), compressed.Bytes()...)
	cn90PVFDecrypt("BodY", encrypted, 0x269EC3)

	archive := &cn90PVFArchive{
		raw:       encrypted,
		groups:    []cn90PVFGroup{{compressed: len(encrypted), original: 16}},
		chunkData: map[int][]byte{},
	}
	if _, err := archive.chunk(0); err == nil {
		t.Fatal("chunk accepted a stream larger than the declared original size")
	}
}

func TestChunkRejectsInvalidDeclaredSize(t *testing.T) {
	var compressed bytes.Buffer
	writer := zlib.NewWriter(&compressed)
	if _, err := writer.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	encrypted := append([]byte(nil), compressed.Bytes()...)
	cn90PVFDecrypt("BodY", encrypted, 0x269EC3)

	archive := &cn90PVFArchive{
		raw:       encrypted,
		groups:    []cn90PVFGroup{{compressed: len(encrypted), original: -1}},
		chunkData: map[int][]byte{},
	}
	if _, err := archive.chunk(0); err == nil {
		t.Fatal("chunk accepted a negative declared size")
	}
}
