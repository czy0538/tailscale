// Copyright (c) Tailscale Inc & AUTHORS
// SPDX-License-Identifier: BSD-3-Clause

//go:build !windows && !js

package safesocket

import (
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

func connect(s *ConnectionStrategy) (net.Conn, error) {
	if runtime.GOOS == "js" {
		return nil, errors.New("safesocket.Connect not yet implemented on js/wasm")
	}
	return net.Dial("unix", s.path)
}

func listen(path string) (net.Listener, error) {
	// Unix sockets hang around in the filesystem even after nobody
	// is listening on them. (Which is really unfortunate but long-
	// entrenched semantics.) Try connecting first; if it works, then
	// the socket is still live, so let's not replace it. If it doesn't
	// work, then replace it.
	//
	// Note that there's a race condition between these two steps. A
	// "proper" daemon usually uses a dance involving pidfiles to first
	// ensure that no other instances of itself are running, but that's
	// beyond the scope of our simple socket library.
	// 1. 尝试连接当前的 unix socket
	c, err := net.Dial("unix", path)

	// 2. 如果连接成功了，说明当前有活跃的连接
	if err == nil {
		// 关闭建立的连接，因为这个链接不是我们想要的
		c.Close()
		// 看看这个连接是不是我们 Tailscale 造成的，bing 返回相应的错误提示。
		if tailscaledRunningUnderLaunchd() {
			return nil, fmt.Errorf("%v: address already in use; tailscaled already running under launchd (to stop, run: $ sudo launchctl stop com.tailscale.tailscaled)", path)
		}
		return nil, fmt.Errorf("%v: address already in use", path)
	}

	// 3. 如果连接失败了，可能仍留在文件系统中，因此需要替换他
	_ = os.Remove(path)

	// 4.获取文件需要的权限
	perm := socketPermissionsForOS()

	sockDir := filepath.Dir(path)
	if _, err := os.Stat(sockDir); os.IsNotExist(err) {
		os.MkdirAll(sockDir, 0755) // best effort

		// If we're on a platform where we want the socket
		// world-readable, open up the permissions on the
		// just-created directory too, in case a umask ate
		// it. This primarily affects running tailscaled by
		// hand as root in a shell, as there is no umask when
		// running under systemd.
		if perm == 0666 {
			if fi, err := os.Stat(sockDir); err == nil && fi.Mode()&0077 == 0 {
				if err := os.Chmod(sockDir, 0755); err != nil {
					log.Print(err)
				}
			}
		}
	}
	// 侦听当前 socket
	pipe, err := net.Listen("unix", path)
	if err != nil {
		return nil, err
	}
	os.Chmod(path, perm)
	return pipe, err
}

func tailscaledRunningUnderLaunchd() bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	plist, err := exec.Command("launchctl", "list", "com.tailscale.tailscaled").Output()
	_ = plist // parse it? https://github.com/DHowett/go-plist if we need something.
	running := err == nil
	return running
}

// socketPermissionsForOS returns the permissions to use for the
// tailscaled.sock.
func socketPermissionsForOS() os.FileMode {
	if PlatformUsesPeerCreds() {
		return 0666
	}
	// Otherwise, root only.
	return 0600
}
