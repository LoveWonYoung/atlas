package can_driver

import (
	"errors"
	"testing"
)

func TestSharedSessionClosesAfterLastRelease(t *testing.T) {
	var session sharedSession
	openCalls := 0
	closeCalls := 0
	openResource := func() error {
		openCalls++
		return nil
	}
	closeResource := func() error {
		closeCalls++
		return nil
	}

	if err := session.acquire(openResource); err != nil {
		t.Fatal(err)
	}
	if err := session.acquire(openResource); err != nil {
		t.Fatal(err)
	}
	if openCalls != 1 {
		t.Fatalf("open calls = %d, want 1", openCalls)
	}

	if err := session.release(closeResource); err != nil {
		t.Fatal(err)
	}
	if closeCalls != 0 {
		t.Fatalf("close calls after first release = %d, want 0", closeCalls)
	}
	if !session.isOpened() {
		t.Fatal("session closed while another holder was active")
	}

	if err := session.release(closeResource); err != nil {
		t.Fatal(err)
	}
	if closeCalls != 1 {
		t.Fatalf("close calls after final release = %d, want 1", closeCalls)
	}
	if session.isOpened() {
		t.Fatal("session remained open after final release")
	}
}

func TestSharedSessionRetriesFailedCloseWithoutReopening(t *testing.T) {
	var session sharedSession
	openCalls := 0
	closeCalls := 0
	closeErr := errors.New("close failed")
	openResource := func() error {
		openCalls++
		return nil
	}

	if err := session.acquire(openResource); err != nil {
		t.Fatal(err)
	}
	if err := session.release(func() error {
		closeCalls++
		return closeErr
	}); !errors.Is(err, closeErr) {
		t.Fatalf("release error = %v, want %v", err, closeErr)
	}
	if !session.isOpened() {
		t.Fatal("failed close should leave the session marked open")
	}

	if err := session.acquire(openResource); err != nil {
		t.Fatal(err)
	}
	if openCalls != 1 {
		t.Fatalf("open calls = %d, want 1 after re-acquire", openCalls)
	}
	if err := session.release(func() error {
		closeCalls++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if closeCalls != 2 {
		t.Fatalf("close calls = %d, want 2", closeCalls)
	}
	if session.isOpened() {
		t.Fatal("session remained open after successful retry")
	}
}

func TestSharedSessionDoesNotRetainFailedOpen(t *testing.T) {
	var session sharedSession
	openErr := errors.New("open failed")

	if err := session.acquire(func() error { return openErr }); !errors.Is(err, openErr) {
		t.Fatalf("acquire error = %v, want %v", err, openErr)
	}
	if session.isOpened() {
		t.Fatal("failed open left the session marked open")
	}

	openCalls := 0
	if err := session.acquire(func() error {
		openCalls++
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if openCalls != 1 {
		t.Fatalf("open calls = %d, want 1", openCalls)
	}
}
