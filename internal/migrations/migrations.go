package migrations

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"strings"

	"github.com/sampsonlor/ovs-webui/internal/repository"
)

//go:embed web/*.sql manager/*.sql
var files embed.FS

type Migration struct {
	Version       int
	SQL, Checksum string
}

func For(kind repository.Kind) []Migration {
	if kind != repository.Web && kind != repository.Manager {
		return nil
	}
	names, err := files.ReadDir(string(kind))
	if err != nil {
		panic("missing embedded migrations")
	}
	result := make([]Migration, 0, len(names))
	for i, name := range names {
		if !strings.HasPrefix(name.Name(), fmt.Sprintf("%03d_", i+1)) {
			panic("migration sequence gap")
		}
		b, err := files.ReadFile(string(kind) + "/" + name.Name())
		if err != nil {
			panic("missing migration")
		}
		sql := strings.ReplaceAll(string(b), "\r\n", "\n")
		sum := sha256.Sum256([]byte(sql))
		result = append(result, Migration{Version: i + 1, SQL: sql, Checksum: hex.EncodeToString(sum[:])})
	}
	return result
}
