// Copyright (C) 2026 Thinline Dynamic Solutions
//
// This program is free software: you can redistribute it and/or modify
// it under the terms of the GNU General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.

//go:build netbsd || solaris

package main

import "golang.org/x/sys/unix"

// diskUsage returns the bytes available to this process and the total size of
// the filesystem holding path.
func diskUsage(path string) (free, total uint64, err error) {
	var st unix.Statvfs_t
	if err = unix.Statvfs(path, &st); err != nil {
		return 0, 0, err
	}
	frsize := uint64(st.Frsize)
	return uint64(st.Bavail) * frsize, uint64(st.Blocks) * frsize, nil
}
