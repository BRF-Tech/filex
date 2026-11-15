//go:build windows

package main

import (
	"context"

	"golang.org/x/sys/windows/svc"
)

// isWindowsService reports whether the service manager started this process
// (New-Service ... ; Start-Service filex-watch, docs/FILEX-WATCH.md).
func isWindowsService() bool {
	ok, err := svc.IsWindowsService()
	return err == nil && ok
}

// runService runs work as the Windows service "filex-watch": stopped when
// the service manager asks (Stop, or the machine shutting down).
func runService(work func(ctx context.Context) error) error {
	return svc.Run("filex-watch", &service{work: work})
}

type service struct {
	work func(ctx context.Context) error
}

// Execute is svc.Handler.
func (s *service) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.work(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending}
				cancel()
				<-done
				return false, 0
			}
		case err := <-done:
			if err != nil {
				return false, 1
			}
			return false, 0
		}
	}
}
