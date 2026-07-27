package can_driver

import "sync"

// sharedSession keeps a shared hardware resource open until the last holder
// releases it. A failed close leaves the resource marked open so a later
// acquire/release pair can retry cleanup without reopening the hardware.
type sharedSession struct {
	mu     sync.Mutex
	users  int
	opened bool
}

func (s *sharedSession) acquire(open func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.users > 0 {
		s.users++
		return nil
	}
	if !s.opened {
		if err := open(); err != nil {
			return err
		}
		s.opened = true
	}
	s.users = 1
	return nil
}

func (s *sharedSession) release(closeResource func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.users == 0 {
		return nil
	}
	s.users--
	if s.users > 0 || !s.opened {
		return nil
	}
	if err := closeResource(); err != nil {
		return err
	}
	s.opened = false
	return nil
}

func (s *sharedSession) isOpened() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.opened
}
