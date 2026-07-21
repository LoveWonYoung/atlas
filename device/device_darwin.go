//go:build darwin && cgo

package device

import (
	"fmt"

	"github.com/LoveWonYoung/atlas/lindriver"
)

func initDevice(config Config) (*Device, error) {
	if config.Bus == BusCAN {
		if config.Provider != ProviderAuto && config.Provider != ProviderToomoss {
			return nil, fmt.Errorf("device: provider %q does not support CAN on macOS", config.Provider)
		}
		return initializedCAN(newDarwinToomossCAN(config.CAN.Type, config.CAN.Channel))
	}

	switch config.Provider {
	case ProviderAuto, ProviderToomoss:
		lindriver.Bt = config.LIN.BaudRate
		dev, err := lindriver.NewToomoss(config.LIN.Channels, nativeLINMode(config.LIN.Mode))
		if err != nil {
			return nil, err
		}
		return initializedLIN(dev), nil
	case ProviderMock:
		return initializedLIN(lindriver.NewMockDriver()), nil
	default:
		return nil, fmt.Errorf("device: provider %q does not support LIN on macOS", config.Provider)
	}
}
