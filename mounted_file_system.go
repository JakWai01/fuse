// Copyright 2015 Google Inc. All Rights Reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package fuse

import (
	"errors"

	"github.com/jacobsa/bazilfuse"
	"golang.org/x/net/context"
)

// A struct representing the status of a mount operation, with methods for
// waiting on the mount to complete, waiting for unmounting, and causing
// unmounting.
type MountedFileSystem struct {
	dir string

	// The result to return from WaitForReady. Not valid until the channel is
	// closed.
	readyStatus          error
	readyStatusAvailable chan struct{}

	// The result to return from Join. Not valid until the channel is closed.
	joinStatus          error
	joinStatusAvailable chan struct{}
}

// Return the directory on which the file system is mounted (or where we
// attempted to mount it.)
func (mfs *MountedFileSystem) Dir() string {
	return mfs.dir
}

// Wait until the mount point is ready to be used. After a successful return
// from this function, the contents of the mounted file system should be
// visible in the directory supplied to NewMountPoint. May be called multiple
// times.
func (mfs *MountedFileSystem) WaitForReady(ctx context.Context) error {
	select {
	case <-mfs.readyStatusAvailable:
		return mfs.readyStatus
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Block until a mounted file system has been unmounted. The return value will
// be non-nil if anything unexpected happened while serving. May be called
// multiple times. Must not be called unless WaitForReady has returned nil.
func (mfs *MountedFileSystem) Join(ctx context.Context) error {
	select {
	case <-mfs.joinStatusAvailable:
		return mfs.joinStatus
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Attempt to unmount the file system. Use Join to wait for it to actually be
// unmounted. You must first call WaitForReady to ensure there is no race with
// mounting.
func (mfs *MountedFileSystem) Unmount() error {
	return bazilfuse.Unmount(mfs.dir)
}

// Runs in the background.
func (mfs *MountedFileSystem) mountAndServe(
	server *server,
	options []bazilfuse.MountOption) {
	logger := getLogger()

	// Open a FUSE connection.
	logger.Println("Opening a FUSE connection.")
	c, err := bazilfuse.Mount(mfs.dir, options...)
	if err != nil {
		mfs.readyStatus = errors.New("bazilfuse.Mount: " + err.Error())
		close(mfs.readyStatusAvailable)
		return
	}

	defer c.Close()

	// Start a goroutine that will notify the MountedFileSystem object when the
	// connection says it is ready (or it fails to become ready).
	go func() {
		logger.Println("Waiting for the FUSE connection to be ready.")
		<-c.Ready
		logger.Println("The FUSE connection is ready.")

		mfs.readyStatus = c.MountError
		close(mfs.readyStatusAvailable)
	}()

	// Serve the connection using the file system object.
	logger.Println("Serving the FUSE connection.")
	if err := server.Serve(c); err != nil {
		mfs.joinStatus = errors.New("Serve: " + err.Error())
		close(mfs.joinStatusAvailable)
		return
	}

	// Signal that everything is okay.
	close(mfs.joinStatusAvailable)
}

// Optional configuration accepted by Mount.
type MountConfig struct {
}

// Convert to mount options to be passed to package bazilfuse.
func (c *MountConfig) bazilfuseOptions() []bazilfuse.MountOption

// Attempt to mount the supplied file system on the given directory.
// mfs.WaitForReady() must be called to find out whether the mount was
// successful.
func Mount(
	dir string,
	fs FileSystem,
	config *MountConfig) (mfs *MountedFileSystem, err error) {
	// Create a server object.
	server, err := newServer(fs)
	if err != nil {
		return
	}

	// Initialize the struct.
	mfs = &MountedFileSystem{
		dir:                  dir,
		readyStatusAvailable: make(chan struct{}),
		joinStatusAvailable:  make(chan struct{}),
	}

	// Mount in the background.
	go mfs.mountAndServe(server, config.bazilfuseOptions())

	return
}
