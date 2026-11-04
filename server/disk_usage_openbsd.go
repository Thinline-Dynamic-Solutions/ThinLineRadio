// Copyright (C) 2026 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

package main

import "syscall"

// diskUsage returns the bytes available to this process and the total size of
// the filesystem holding path.
func diskUsage(path string) (free, total uint64, err error) {
	var st syscall.Statfs_t
	if err = syscall.Statfs(path, &st); err != nil {
		return 0, 0, err
	}
	bsize := uint64(st.F_bsize)
	var avail uint64
	if st.F_bavail > 0 {
		avail = uint64(st.F_bavail)
	}
	return avail * bsize, st.F_blocks * bsize, nil
}
