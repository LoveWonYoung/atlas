//go:build !windows && (!darwin || !cgo)

package device

import (
	"fmt"

	"github.com/LoveWonYoung/atlas/lindriver"
)

func initDevice(config Config) (*Device, error) {
	if config.Bus == BusLIN && config.Provider == ProviderMock {
		return initializedLIN(lindriver.NewMockDriver()), nil
	}
	return nil, fmt.Errorf("device: provider %q does not support %s on this platform", config.Provider, config.Bus)
}
