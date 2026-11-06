package receiver

import (
	"fmt"
	"os"
	"sync"

	"github.com/gokrazy/rsync"
)

type hardLinkKey struct {
	dev   uint64
	inode uint64
}

// hardLinkGroup collects all file-list names that point at the same inode on
// the sender. The first (lowest-name) member is the master: its content is
// transferred exactly once, and every other name (the companions) is created
// as a hard link to the master name on disk.
type hardLinkGroup struct {
	master int
	// masterName is the destination-relative name of the master file.
	masterName string
	// ready is closed once the master name exists on disk with up-to-date
	// content (either it was already present and unchanged, or the receiver
	// goroutine finished writing it).
	ready chan struct{}
	once  sync.Once
}

func (g *hardLinkGroup) markReady() {
	g.once.Do(func() { close(g.ready) })
}

// initHardLinkGroups groups the received file list by source (dev, inode)
// identity, mirroring rsync/flist.c:match_hard_links for protocol versions
// below 28 (protocol 27 sends dev/inode with every regular file and lets the
// receiver figure out the groups). Only groups with at least two names are
// recorded; files with unique inodes are transferred normally.
//
// Because grouping is based on the source inode identity, independent files
// can never end up merged.
func (rt *Transfer) initHardLinkGroups(fileList []*File) {
	if !rt.Opts.PreserveHardlinks {
		return
	}
	rt.hardLinkGroups = make(map[int]*hardLinkGroup)
	rt.hardLinkAbort = make(chan struct{})

	byKey := make(map[hardLinkKey][]int)
	for idx, f := range fileList {
		if f.Mode&rsync.S_IFMT != rsync.S_IFREG {
			continue
		}
		key := hardLinkKey{dev: f.Dev, inode: f.Inode}
		byKey[key] = append(byKey[key], idx)
	}
	for _, members := range byKey {
		if len(members) < 2 {
			continue
		}
		// The file list is sorted by name and members were appended in
		// index order, so members[0] is the lowest-name index, i.e. the
		// master (rsync's "first" file of the hard-link set).
		g := &hardLinkGroup{
			master:     members[0],
			masterName: fileList[members[0]].Name,
			ready:      make(chan struct{}),
		}
		for _, idx := range members {
			rt.hardLinkGroups[idx] = g
		}
	}
}

// hardLinkMasterReady is called from the receiver goroutine once the file at
// idx was received (or skipped in dry-run). Only the group master unblocks
// its waiting companions.
func (rt *Transfer) hardLinkMasterReady(idx int) {
	if g, ok := rt.hardLinkGroups[idx]; ok && idx == g.master {
		g.markReady()
	}
}

// recvHardLinkCompanion handles a non-master name of a hard-link group. It
// never requests file content over the network: it waits until the master
// name is in place, then turns this name into a hard link of the master (or
// leaves it untouched if it already is).
//
// Corresponds to rsync/generator.c:hard_link_check().
func (rt *Transfer) recvHardLinkCompanion(f *File, g *hardLinkGroup) error {
	if rt.Opts.DryRun {
		// In dry-run mode, do not create or modify anything.
		return nil
	}

	// Wait until the master file exists with its final content. The abort
	// channel ensures this cannot block a failing transfer forever.
	select {
	case <-g.ready:
	case <-rt.hardLinkAbort:
		return nil
	}

	return rt.linkNameToMaster(f.Name, g.masterName)
}

// linkNameToMaster makes dst a hard link of src. If dst already shares the
// same inode as src, it is left completely untouched (so its ctime is
// preserved across re-syncs). Otherwise an existing independent copy or a
// wrong-typed file is removed before creating the link.
func (rt *Transfer) linkNameToMaster(dst, src string) error {
	mst, err := rt.DestRoot.Lstat(src)
	if err != nil {
		return fmt.Errorf("hard link: stat master %s: %v", src, err)
	}
	st, err := rt.DestRoot.Lstat(dst)
	switch {
	case err == nil:
		if sameInode(mst, st) {
			// Already correctly hard-linked: nothing to do, keep ctime.
			return nil
		}
		if err := rt.DestRoot.Remove(dst); err != nil {
			return fmt.Errorf("unlinking %s to make room for a hard link: %v", dst, err)
		}
	case os.IsNotExist(err):
		// Name does not exist yet, nothing to remove.
	default:
		return err
	}
	if err := rt.DestRoot.Link(src, dst); err != nil {
		return fmt.Errorf("creating hard link %s => %s: %v", dst, src, err)
	}
	return nil
}
