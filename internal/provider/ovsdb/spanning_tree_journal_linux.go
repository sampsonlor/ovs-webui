//go:build linux

package ovsdb

import (
	"context"
	"fmt"
	"os"

	"github.com/sampsonlor/ovs-webui/internal/candidate"
	"github.com/sampsonlor/ovs-webui/internal/inventory"
	"golang.org/x/sys/unix"
)

// Native monitors may coalesce a change followed by restoration. The pinned
// standalone OVSDB server appends and flushes persistent transactions before
// completing them; ephemeral statistics are omitted. A held, unchanged journal
// therefore supplies the independent ABA fence. Clustered/memory storage and
// file replacement/compaction are not qualified by this execution path.
type treeJournal struct {
	file       *os.File
	before     unix.Stat_t
	executable unix.Stat_t
	authority  string
}

func (e *Executor) openTreeJournal(ctx context.Context, pid int, evidence inventory.Evidence) (*treeJournal, error) {
	if e.spanningTree == nil {
		return nil, witnessUnproven
	}
	o := e.spanningTree
	hash, executable, err := witnessFileHash(ctx, fmt.Sprintf("/proc/%d/exe", pid), 128<<20, true)
	var configured unix.Stat_t
	if err != nil || hash != o.DatabaseSHA256 || unix.Stat(o.DatabaseExecutable, &configured) != nil || !witnessSameFile(executable, configured) {
		return nil, witnessUnproven
	}
	fd, err := unix.Open(e.provider.options.DatabaseFile, unix.O_RDONLY|unix.O_NOFOLLOW|unix.O_CLOEXEC|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, witnessUnproven
	}
	j := &treeJournal{file: os.NewFile(uintptr(fd), "tree-journal"), executable: executable}
	var header [11]byte
	n, readErr := j.file.ReadAt(header[:], 0)
	if unix.Fstat(fd, &j.before) != nil || j.before.Mode&unix.S_IFMT != unix.S_IFREG || j.before.Mode&0022 != 0 || j.before.Uid != o.PeerUID || !evidence.File.Available || uint64(j.before.Dev) != evidence.File.Device || j.before.Ino != evidence.File.Inode || readErr != nil || n != len(header) || string(header[:]) != "OVSDB JSON " {
		_ = j.file.Close()
		return nil, witnessUnproven
	}
	j.authority = candidate.Digest([]any{evidence.Peer, evidence.Boot, uint64(j.before.Dev), j.before.Ino, hash, executable.Dev, executable.Ino, executable.Size, executable.Mtim, executable.Ctim})
	if j.check(e, pid, evidence) != nil || ctx.Err() != nil {
		_ = j.file.Close()
		return nil, witnessUnproven
	}
	return j, nil
}

func (j *treeJournal) check(e *Executor, pid int, evidence inventory.Evidence) error {
	var held, named, executable, configured unix.Stat_t
	if unix.Fstat(int(j.file.Fd()), &held) != nil || unix.Lstat(e.provider.options.DatabaseFile, &named) != nil || !witnessSameFile(j.before, held) || !witnessSameFile(j.before, named) || unix.Stat(fmt.Sprintf("/proc/%d/exe", pid), &executable) != nil || unix.Stat(e.spanningTree.DatabaseExecutable, &configured) != nil || !witnessSameFile(j.executable, executable) || !witnessSameFile(j.executable, configured) {
		return treeFenceChanged
	}
	w := witness(e.provider.options.DatabaseFile, pid, evidence.File)
	if !w.Available || !w.ServerHasFile || w.ServerBinding != "observed-file-handle" || !w.PriorMatches || uint64(held.Dev) != w.Device || held.Ino != w.Inode || held.Size != w.Size {
		return treeFenceChanged
	}
	return nil
}
