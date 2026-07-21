//go:build windows

package preset

import "github.com/LoveWonYoung/atlas/driver"

func NewPresetAuto(physId, respId, funcId uint32, canType driver.CanType) (*Preset, error) {
	return newPreset(driver.NewAutoDriver(canType), physId, respId, funcId, canType == driver.CANFD)
}
