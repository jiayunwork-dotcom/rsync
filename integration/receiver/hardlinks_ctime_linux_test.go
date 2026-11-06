//go:build linux

package receiver_test

import "syscall"

func hlCtimeTimespec(st *syscall.Stat_t) syscall.Timespec {
	return st.Ctim
}
