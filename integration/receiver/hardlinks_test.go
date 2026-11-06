package receiver_test

// Tests for hard link preservation (-H / --hard-links), covering both
// directions (pulling from a gokr-rsync daemon and pushing to a writable one).

import (
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/gokrazy/rsync/internal/rsyncstats"
	"github.com/gokrazy/rsync/internal/rsynctest"
)

const (
	hlMiB    = int64(1024 * 1024)
	hlMarker = byte(0x5a)
)

// hlInode returns the (device, inode) pair of a path.
func hlInode(t *testing.T, path string) (dev, inode uint64) {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return uint64(st.Dev), uint64(st.Ino)
}

// hlCtime returns the inode change time of a path.
func hlCtime(t *testing.T, path string) syscall.Timespec {
	t.Helper()
	var st syscall.Stat_t
	if err := syscall.Stat(path, &st); err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return hlCtimeTimespec(&st)
}

// hlMakeSource builds a source tree in which dir1/a, dir2/b and dir3/c are
// hard links of the same 1 MiB inode (the names live in different
// subdirectories), while dir1/solo is an independent file with its own inode.
func hlMakeSource(t *testing.T, source string, content []byte) {
	t.Helper()
	for _, d := range []string{"dir1", "dir2", "dir3"} {
		if err := os.MkdirAll(filepath.Join(source, d), 0755); err != nil {
			t.Fatal(err)
		}
	}
	a := filepath.Join(source, "dir1", "a")
	if err := os.WriteFile(a, content, 0644); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{
		filepath.Join("dir2", "b"),
		filepath.Join("dir3", "c"),
	} {
		if err := os.Link(a, filepath.Join(source, name)); err != nil {
			t.Fatal(err)
		}
	}
	solo := filepath.Join(source, "dir1", "solo")
	if err := os.WriteFile(solo, []byte("independent file"), 0644); err != nil {
		t.Fatal(err)
	}
	mt := time.Unix(1_500_000_000, 0)
	for _, p := range []string{
		a,
		filepath.Join(source, "dir2", "b"),
		filepath.Join(source, "dir3", "c"),
		solo,
		filepath.Join(source, "dir1"),
		filepath.Join(source, "dir2"),
		filepath.Join(source, "dir3"),
	} {
		if err := os.Chtimes(p, mt, mt); err != nil {
			t.Fatal(err)
		}
	}
}

func hlContent(marker byte) []byte {
	b := make([]byte, hlMiB)
	for i := range b {
		b[i] = marker
	}
	return b
}

// hlSetup prepares source and destination trees plus a sync() closure for the
// requested direction.
func hlSetup(t *testing.T, direction string) (source, dest string, sync func(args ...string) *rsyncstats.TransferStats) {
	t.Helper()
	tmp := t.TempDir()
	source = filepath.Join(tmp, "source")
	dest = filepath.Join(tmp, "dest")
	if err := os.MkdirAll(source, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	hlMakeSource(t, source, hlContent(hlMarker))

	switch direction {
	case "pull":
		srv := rsynctest.New(t, rsynctest.InteropModule(source))
		sync = func(args ...string) *rsyncstats.TransferStats {
			full := append([]string{"gokr-rsync"}, args...)
			full = append(full, "rsync://localhost:"+srv.Port+"/interop/", dest)
			return rsynctest.Run(t, full...)
		}
	case "push":
		srv := rsynctest.New(t, rsynctest.WritableInteropModule(dest))
		sync = func(args ...string) *rsyncstats.TransferStats {
			full := append([]string{"gokr-rsync"}, args...)
			full = append(full, source+"/", "rsync://localhost:"+srv.Port+"/interop/")
			return rsynctest.Run(t, full...)
		}
	default:
		t.Fatalf("unknown direction %q", direction)
	}
	return source, dest, sync
}

// names returns the destination paths of all four regular files.
func hlNames(dest string) (a, b, c, solo string) {
	return filepath.Join(dest, "dir1", "a"),
		filepath.Join(dest, "dir2", "b"),
		filepath.Join(dest, "dir3", "c"),
		filepath.Join(dest, "dir1", "solo")
}

func hlAssertLinked(t *testing.T, dest, wantContent string) {
	t.Helper()
	a, b, c, solo := hlNames(dest)
	da, ia := hlInode(t, a)
	db, ib := hlInode(t, b)
	dc, ic := hlInode(t, c)
	ds, is := hlInode(t, solo)

	if (devInode{da, ia}) != (devInode{db, ib}) {
		t.Errorf("dir1/a (%d,%d) and dir2/b (%d,%d) are not hard-linked", da, ia, db, ib)
	}
	if (devInode{da, ia}) != (devInode{dc, ic}) {
		t.Errorf("dir1/a (%d,%d) and dir3/c (%d,%d) are not hard-linked", da, ia, dc, ic)
	}
	// The independent file must not have been merged into the group, even
	// though it was transferred in the same run.
	if ds == da && is == ia {
		t.Errorf("independent dir1/solo was mistakenly merged into the hard-link group of dir1/a (inode %d,%d)", ds, is)
	}
	for _, p := range []string{a, b, c} {
		got, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != wantContent {
			t.Errorf("content of %s does not match", p)
		}
	}
}

type devInode struct{ dev, inode uint64 }

func TestHardLinks(t *testing.T) {
	t.Parallel()

	for _, direction := range []string{"pull", "push"} {
		t.Run(direction, func(t *testing.T) {
			t.Parallel()
			source, dest, sync := hlSetup(t, direction)
			content := string(hlContent(hlMarker))

			// 1. First sync: links must be preserved, and the inode content
			// must cross the network only once.
			stats := sync("-aH")
			hlAssertLinked(t, dest, content)
			if got, want := stats.Read+stats.Written, int64(1.5*float64(hlMiB)); got >= want {
				t.Fatalf("hard-linked inode transferred more than once: read+written = %d bytes, want < %d", got, want)
			}

			// 2. Unchanged second sync: no names are deleted/recreated, so
			// their ctime must be preserved.
			a, b, c, _ := hlNames(dest)
			ctimeBefore := map[string]syscall.Timespec{
				a: hlCtime(t, a),
				b: hlCtime(t, b),
				c: hlCtime(t, c),
			}
			// Sleep long enough that any accidental recreation would be
			// visible even on filesystems with coarse ctime granularity.
			time.Sleep(1100 * time.Millisecond)
			stats2 := sync("-aH")
			if stats2.Read+stats2.Written >= 64*1024 {
				t.Fatalf("unchanged re-sync unexpectedly transferred data: read+written = %d bytes", stats2.Read+stats2.Written)
			}
			for p, want := range ctimeBefore {
				if got := hlCtime(t, p); got != want {
					t.Errorf("ctime of %s changed on unchanged re-sync: got %v, want %v", p, got, want)
				}
			}

			// 3. One destination name turned into an independent copy: it
			// must be removed and re-linked to the master on the next sync.
			time.Sleep(1100 * time.Millisecond)
			if err := os.Remove(b); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(b, []byte(content), 0644); err != nil {
				t.Fatal(err)
			}
			bCTimeBefore := hlCtime(t, b)
			sync("-aH")
			_, ia := hlInode(t, a)
			_, ib := hlInode(t, b)
			_, ic := hlInode(t, c)
			if ia != ib {
				t.Errorf("independent copy of dir2/b was not re-linked: a inode=%d, b inode=%d", ia, ib)
			}
			if ib != ic {
				t.Errorf("dir2/b was re-linked but dir3/c no longer shares the inode: b=%d c=%d", ib, ic)
			}
			if got := hlCtime(t, b); got == bCTimeBefore {
				t.Errorf("ctime of rebuilt dir2/b unexpectedly unchanged")
			}
			// Note: link(2) bumps the ctime of the shared inode itself (the
			// link count changed), which is visible through every name; that
			// is expected rsync behavior.

			// 4. Source content update: all names must end up sharing the
			// new inode with the new content.
			newContent := hlContent(0xa5)
			af := filepath.Join(source, "dir1", "a")
			// Truncating and rewriting keeps the source inode (all three
			// source names see the new content).
			if err := os.WriteFile(af, newContent, 0644); err != nil {
				t.Fatal(err)
			}
			newMtime := time.Unix(1_600_000_000, 0)
			if err := os.Chtimes(af, newMtime, newMtime); err != nil {
				t.Fatal(err)
			}
			sync("-aH")
			hlAssertLinked(t, dest, string(newContent))

			// 5. Dry run must not create or modify anything.
			dryDest := filepath.Join(t.TempDir(), "dest")
			if err := os.MkdirAll(dryDest, 0755); err != nil {
				t.Fatal(err)
			}
			drySource := filepath.Join(t.TempDir(), "source")
			if err := os.MkdirAll(drySource, 0755); err != nil {
				t.Fatal(err)
			}
			hlMakeSource(t, drySource, newContent)
			var drySrvPort string
			switch direction {
			case "pull":
				srv := rsynctest.New(t, rsynctest.InteropModule(drySource))
				drySrvPort = srv.Port
			case "push":
				srv := rsynctest.New(t, rsynctest.WritableInteropModule(dryDest))
				drySrvPort = srv.Port
			}
			var dryArgs []string
			switch direction {
			case "pull":
				dryArgs = []string{"gokr-rsync", "-aHn", "rsync://localhost:" + drySrvPort + "/interop/", dryDest}
			case "push":
				dryArgs = []string{"gokr-rsync", "-aHn", drySource + "/", "rsync://localhost:" + drySrvPort + "/interop/"}
			}
			rsynctest.Run(t, dryArgs...)
			entries, err := os.ReadDir(dryDest)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Fatalf("dry run modified the destination: found %v", entries)
			}
		})
	}
}

func TestHardLinksDisabledWithoutFlag(t *testing.T) {
	t.Parallel()

	for _, direction := range []string{"pull", "push"} {
		t.Run(direction, func(t *testing.T) {
			t.Parallel()
			_, dest, sync := hlSetup(t, direction)

			// Note: -a does not include -H.
			sync("-a")

			a, b, c, _ := hlNames(dest)
			_, ia := hlInode(t, a)
			_, ib := hlInode(t, b)
			_, ic := hlInode(t, c)
			if ia == ib || ia == ic || ib == ic {
				t.Fatalf("without -H, destination names must be independent: a=%d b=%d c=%d", ia, ib, ic)
			}
		})
	}
}
