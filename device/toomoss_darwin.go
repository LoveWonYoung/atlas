//go:build darwin && cgo

package device

import "github.com/LoveWonYoung/atlas/driver"

func newDarwinToomossCAN(canType driver.CanType, channel byte) driver.CANDriver {
	return driver.NewToomoss(canType, channel)
}
