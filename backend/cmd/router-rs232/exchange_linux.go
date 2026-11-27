package main

import (
	"context"
	"errors"
	"fmt"

	"golang.org/x/sys/unix"
)

func exchange(ctx context.Context, path string, speed uint32, tx, rx []byte) (result error) {
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_NOCTTY|unix.O_NONBLOCK|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", path, err)
	}
	defer unix.Close(fd)
	if err := unix.Flock(fd, unix.LOCK_EX|unix.LOCK_NB); err != nil {
		return fmt.Errorf("lock %s: %w", path, err)
	}
	if err := unix.IoctlSetInt(fd, unix.TIOCEXCL, 0); err != nil {
		return fmt.Errorf("reserve %s: %w", path, err)
	}
	defer unix.IoctlSetInt(fd, unix.TIOCNXCL, 0)
	original, err := unix.IoctlGetTermios(fd, unix.TCGETS)
	if err != nil {
		return fmt.Errorf("read port settings: %w", err)
	}
	raw := *original
	raw.Iflag, raw.Oflag, raw.Lflag = unix.IGNBRK|unix.IGNPAR, 0, 0
	raw.Cflag = unix.CS8 | unix.CLOCAL | unix.CREAD | speed
	raw.Ispeed, raw.Ospeed = speed, speed
	raw.Cc[unix.VMIN], raw.Cc[unix.VTIME] = 1, 0
	if err := unix.IoctlSetTermios(fd, unix.TCSETS, &raw); err != nil {
		return fmt.Errorf("configure port: %w", err)
	}
	defer func() {
		if err := unix.IoctlSetTermios(fd, unix.TCSETS, original); err != nil {
			result = errors.Join(result, fmt.Errorf("restore port settings: %w", err))
		}
	}()
	if err := unix.IoctlSetInt(fd, unix.TCFLSH, unix.TCIOFLUSH); err != nil {
		return fmt.Errorf("clear port queues: %w", err)
	}
	if err := transfer(ctx, fd, unix.POLLOUT, tx, unix.Write); err != nil {
		return fmt.Errorf("send: %w", err)
	}
	return transfer(ctx, fd, unix.POLLIN, rx, unix.Read)
}

func transfer(ctx context.Context, fd int, event int16, data []byte, io func(int, []byte) (int, error)) error {
	poll := []unix.PollFd{{Fd: int32(fd), Events: event}}
	for offset := 0; offset < len(data); {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("%d/%d bytes: %w", offset, len(data), err)
		}
		_, err := unix.Poll(poll, 20)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if poll[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return fmt.Errorf("serial device disconnected or failed")
		}
		if poll[0].Revents&event == 0 {
			continue
		}
		count, err := io(fd, data[offset:])
		if err == unix.EINTR || err == unix.EAGAIN {
			continue
		}
		if err != nil {
			return err
		}
		offset += count
	}
	return nil
}
