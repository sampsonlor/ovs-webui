package sqlite

import (
	"bytes"
	"encoding/binary"
	"errors"
	"os"
	"testing"
)

func TestBackupComponentStreamsPublishOnlyVerifiedCompletePairs(t *testing.T) {
	manager, web := backupPairStores(t)
	var managerPipe, webPipe bytes.Buffer
	for _, entry := range []struct {
		store *Store
		out   *bytes.Buffer
	}{{manager, &managerPipe}, {web, &webPipe}} {
		if err := entry.store.WriteBackupComponent(t.Context(), entry.out); err != nil {
			t.Fatal(err)
		}
	}
	root := privateBackupPairRoot(t)
	good := managerPipe.Bytes()
	pair, err := PublishBackupPairStreams(t.Context(), root, bytes.NewReader(good), bytes.NewReader(webPipe.Bytes()))
	if err != nil {
		t.Fatal(err)
	}
	verified, err := VerifyBackupPair(t.Context(), root, pair.ID)
	if err != nil || verified != pair {
		t.Fatalf("published stream pair was not verifiable: %+v, %v", verified, err)
	}
	tooLarge := append([]byte{}, good...)
	binary.BigEndian.PutUint64(tooLarge[12:], MaxBackupPairDatabaseBytes+1)
	tampered := append([]byte{}, good...)
	tampered[len(tampered)-1] ^= 1
	for name, malformed := range map[string][]byte{
		"swapped": webPipe.Bytes(), "truncated": good[:len(good)-1],
		"trailing": append(append([]byte{}, good...), 0), "tampered": tampered, "oversize": tooLarge,
	} {
		t.Run(name, func(t *testing.T) {
			root := privateBackupPairRoot(t)
			got, err := PublishBackupPairStreams(t.Context(), root, bytes.NewReader(malformed), bytes.NewReader(webPipe.Bytes()))
			if err == nil || got.ID != "" {
				t.Fatalf("malformed stream published: %+v, %v", got, err)
			}
			if name == "oversize" && !errors.Is(err, ErrBackupPairTooLarge) {
				t.Fatalf("oversize stream returned %v", err)
			}
			files, err := os.ReadDir(root)
			if err != nil || len(files) != 0 {
				t.Fatalf("failed stream retained staged content: %v, %v", files, err)
			}
		})
	}
}
