//go:build windows

package driver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"syscall"
	"time"
	"unsafe"
)

type CanfdInitConfig struct {
	Mode         byte
	ISOCRCEnable byte
	RetrySend    byte
	ResEnable    byte
	NbtBrp       byte
	NbtSeg1      byte
	NbtSeg2      byte
	NbtSjw       byte
	DbtBrp       byte
	DbtSeg1      byte
	DbtSeg2      byte
	DbtSjw       byte
	__Res0       []byte
}

type CanInitConfig struct {
	CanBrp  uint
	CanSjw  byte
	CanBs1  byte
	CanBs2  byte
	CanMode byte
	CanAbom byte
	CanNart byte
	CanRflm byte
	CanTxfp byte
}

type CanMsg struct {
	ID            int32
	TimeStamp     int32
	RemoteFlag    byte
	ExternFlag    byte
	DataLen       byte
	Data          [8]byte
	TimeStampHigh byte
}

type CanfdMsg struct {
	ID        uint32
	DLC       byte
	Flags     byte
	__Res0    byte
	__Res1    byte
	TimeStamp uint32
	Data      [64]byte
}

const (
	//	c.CANChannel  = 0
	SpeedBpsNBT = 500_000
	SpeedBpsDBT = 200_0000
)

const (
	GET_FIRMWARE_INFO = 1
	CAN_MODE_LOOPBACK = 0
	CAN_SEND_MSG      = 1
	CAN_GET_MSG       = 1
	CAN_GET_STATUS    = 0
	CAN_SCH           = 0
	CAN_SUCCESS       = 0
	SendCANIndex      = 0
	ReadCANIndex      = 0
)

const (
	CAN_MSG_FLAG_STD   = 0
	CANFD_MSG_FLAG_BRS = 1 << (iota - 1) // CANFD加速帧标志
	CANFD_MSG_FLAG_ESI                   // CANFD错误状态指示
	CANFD_MSG_FLAG_FDF                   // CANFD帧标志
)

const (
	toomossCANFDIDMaskStandard = 0x7FF
	toomossCANFDIDMaskExtended = 0x1FFFFFFF
	toomossClassicFlagRemote   = 0x01
	toomossClassicFlagChannel  = 0x60
	toomossClassicFlagTx       = 0x80
	toomossClassicFlagExt      = 0x01
	toomossClassicFlagError    = 0x80
)

func defaultCANFDInitConfig() CanfdInitConfig {
	return CanfdInitConfig{
		Mode:         0,
		RetrySend:    1,
		ISOCRCEnable: 1,
		ResEnable:    1,
		NbtBrp:       1,
		NbtSeg1:      59,
		NbtSeg2:      20,
		NbtSjw:       2,
		DbtBrp:       1,
		DbtSeg1:      14,
		DbtSeg2:      5,
		DbtSjw:       2,
	}
}

type Toomoss struct {
	rxChan          chan CanFrame
	fanout          *rxFanout
	ctx             context.Context
	cancel          context.CancelFunc
	cfg             Config
	lifecycle       driverLifecycle
	canType         CanType
	CANChannel      byte
	legacyCAN       bool
	canFDInitConfig CanfdInitConfig
	ownsDevice      bool
}

func NewToomoss(canType CanType, canChannel byte) *Toomoss {
	return NewToomossWithConfig(DefaultConfig(canType, canChannel))
}

func NewToomossWithConfig(cfg Config) *Toomoss {
	ctx, cancel := context.WithCancel(context.Background())
	return &Toomoss{
		ctx:             ctx,
		cancel:          cancel,
		cfg:             cfg,
		canType:         cfg.Mode,
		CANChannel:      cfg.Channel,
		canFDInitConfig: defaultCANFDInitConfig(),
	}
}

func (c *Toomoss) SetCANFDInitConfig(cfg CanfdInitConfig) {
	c.canFDInitConfig = cfg
}

func decodeToomossClassicFlags(remoteFlag, externFlag byte) (channel byte, remote bool, extended bool, errorFrame bool, txEcho bool) {
	channel = (remoteFlag & toomossClassicFlagChannel) >> 5
	remote = (remoteFlag & toomossClassicFlagRemote) != 0
	extended = (externFlag & toomossClassicFlagExt) != 0
	errorFrame = (externFlag & toomossClassicFlagError) != 0
	txEcho = (remoteFlag & toomossClassicFlagTx) != 0
	return
}

func encodeToomossClassicFlags(channel byte, extended bool, remote bool) (remoteFlag byte, externFlag byte) {
	remoteFlag = (channel << 5) & toomossClassicFlagChannel
	if remote {
		remoteFlag |= toomossClassicFlagRemote
	}
	if extended {
		externFlag |= toomossClassicFlagExt
	}
	return remoteFlag, externFlag
}

func toomossDLCToDataLen(rawDLC byte, isFD bool) int {
	maxLen := 8
	if isFD {
		maxLen = 64
	}
	actualLen := int(rawDLC)
	if actualLen > maxLen {
		return maxLen
	}
	return actualLen
}

func (c *Toomoss) Init() error {
	c.lifecycle.opMu.Lock()
	defer c.lifecycle.opMu.Unlock()
	if c.lifecycle.isInitialized() {
		return nil
	}
	cfg, err := normalizeConfig(c.cfg)
	if err != nil {
		return err
	}
	c.cfg = cfg
	c.canType = cfg.Mode
	c.CANChannel = cfg.Channel
	c.legacyCAN = false

	if !acquireToomossSession() {
		return errors.New("another Toomoss driver instance is already using the device")
	}
	c.ownsDevice = true
	opened := false
	cleanup := func(err error) error {
		if opened {
			_ = usbClose()
		}
		if c.ownsDevice {
			releaseToomossSession()
			c.ownsDevice = false
		}
		return err
	}

	if err := ensureToomossLoaded(); err != nil {
		return cleanup(fmt.Errorf("failed to load Toomoss DLLs: %w", err))
	}
	if ok, err := usbScan(); err != nil {
		return cleanup(fmt.Errorf("USB scan failed: %w", err))
	} else if !ok {
		return cleanup(errors.New("USB scan failed: device not found"))
	}
	if ok, err := usbOpen(); err != nil {
		return cleanup(fmt.Errorf("USB open failed: %w", err))
	} else if !ok {
		return cleanup(errors.New("USB open failed"))
	}
	opened = true
	fallback := func(fdErr error) error {
		if err := c.fallbackToLegacyCAN(fdErr); err != nil {
			return cleanup(err)
		}
		c.prepareRuntime()
		c.lifecycle.markInitialized()
		return nil
	}

	if c.canType == CAN {
		c.legacyCAN = true
		log.Println("Toomoss forced classic CAN mode")
		if err := c.initLegacyCANDevice(); err != nil {
			return cleanup(err)
		}
		c.prepareRuntime()
		c.lifecycle.markInitialized()
		return nil
	}
	if CANFDGetCANSpeedArg == 0 || CANFDInit == 0 || CANFDStartGetMsg == 0 || CANFDGetMsg == 0 || CANFDSendMsg == 0 {
		return fallback(errors.New("CAN-FD APIs are not available in USB2XXX.dll"))
	}

	canFDInitConfig := c.canFDInitConfig
	fdSpeed, _, callErr := syscall.SyscallN(
		CANFDGetCANSpeedArg,
		uintptr(DevHandle[DEVIndex]),
		uintptr(unsafe.Pointer(&canFDInitConfig)),
		uintptr(c.cfg.NominalBitrate),
		uintptr(c.cfg.DataBitrate),
	)
	if callErr != 0 {
		return fallback(fmt.Errorf("CANFD_GetCANSpeedArg syscall failed: %w", callErr))
	}
	canfdInit, _, callErr := syscall.SyscallN(
		CANFDInit,
		uintptr(DevHandle[DEVIndex]),
		uintptr(c.CANChannel),
		uintptr(unsafe.Pointer(&canFDInitConfig)),
	)
	if callErr != 0 {
		return fallback(fmt.Errorf("CANFD_Init syscall failed: %w", callErr))
	}
	fdStart, _, callErr := syscall.SyscallN(
		CANFDStartGetMsg,
		uintptr(DevHandle[DEVIndex]),
		uintptr(c.CANChannel),
	)
	if callErr != 0 {
		return fallback(fmt.Errorf("CANFD_StartGetMsg syscall failed: %w", callErr))
	}
	time.Sleep(InitDelay)
	if !(canfdInit == 0 && fdStart == 0 && fdSpeed == 0) {
		return fallback(fmt.Errorf("CAN-FD initialization failed: CANFD_Init=%d, CANFD_StartGetMsg=%d, CANFD_GetCANSpeedArg=%d", canfdInit, fdStart, fdSpeed))
	}
	c.prepareRuntime()
	c.lifecycle.markInitialized()
	log.Println("CAN硬件初始化成功。")
	return nil
}

func (c *Toomoss) prepareRuntime() {
	c.ctx, c.cancel = context.WithCancel(context.Background())
	c.rxChan = make(chan CanFrame, c.cfg.RxBufferSize)
	c.fanout = newRxFanout(c.ctx, c.rxChan)
}

func (c *Toomoss) fallbackToLegacyCAN(fdErr error) error {
	log.Printf("Toomoss CAN-FD initialization failed, fallback to classic CAN: %v", fdErr)
	c.legacyCAN = true
	c.canType = CAN
	c.cfg.Mode = CAN
	if err := c.initLegacyCANDevice(); err != nil {
		return fmt.Errorf("CAN-FD initialization failed (%v), fallback classic CAN initialization failed: %w", fdErr, err)
	}
	return nil
}

func (c *Toomoss) initLegacyCANDevice() error {
	if CANGetCANSpeedArg == 0 || CANInit == 0 || CANStartGetMsg == 0 {
		return errors.New("standard CAN APIs are not available in USB2XXX.dll")
	}

	canInitConfig := CanInitConfig{
		CanMode: 0,
		CanAbom: 0,
		CanNart: 1,
		CanRflm: 0,
		CanTxfp: 1,
		CanBrp:  4,
		CanBs1:  15,
		CanBs2:  5,
		CanSjw:  2,
	}

	ret, _, callErr := syscall.SyscallN(
		CANGetCANSpeedArg,
		uintptr(DevHandle[DEVIndex]),
		uintptr(unsafe.Pointer(&canInitConfig)),
		uintptr(c.cfg.NominalBitrate),
	)
	if callErr != 0 {
		return fmt.Errorf("CAN_GetCANSpeedArg syscall failed: %w", callErr)
	}
	if ret != CAN_SUCCESS {
		return fmt.Errorf("CAN_GetCANSpeedArg returned %d", ret)
	}

	canInitRet, _, callErr := syscall.SyscallN(
		CANInit,
		uintptr(DevHandle[DEVIndex]),
		uintptr(c.CANChannel),
		uintptr(unsafe.Pointer(&canInitConfig)),
	)
	if callErr != 0 {
		return fmt.Errorf("CAN_Init syscall failed: %w", callErr)
	}
	startRet, _, callErr := syscall.SyscallN(
		CANStartGetMsg,
		uintptr(DevHandle[DEVIndex]),
		uintptr(c.CANChannel),
	)
	if callErr != 0 {
		return fmt.Errorf("CAN_StartGetMsg syscall failed: %w", callErr)
	}
	time.Sleep(InitDelay)
	if canInitRet != CAN_SUCCESS || startRet != CAN_SUCCESS {
		return fmt.Errorf("standard CAN initialization failed: CAN_Init=%d, CAN_StartGetMsg=%d", canInitRet, startRet)
	}
	log.Println("Toomoss legacy CAN hardware initialized successfully")
	return nil
}

func (c *Toomoss) Start() {
	c.lifecycle.opMu.Lock()
	defer c.lifecycle.opMu.Unlock()
	if !c.lifecycle.isInitialized() {
		log.Println("Toomoss start called before successful initialization")
		return
	}
	c.drainInitialBuffer()
	if c.lifecycle.start(c.readLoop) {
		log.Println("CAN驱动的中央读取服务已启动...")
	}
}

func (c *Toomoss) Stop() {
	c.lifecycle.opMu.Lock()
	defer c.lifecycle.opMu.Unlock()
	log.Println("正在停止CAN-FD驱动的读取服务...")
	wasInitialized := c.lifecycle.cancelAndWait(c.cancel)
	if c.fanout != nil {
		c.fanout.Close()
		c.fanout = nil
	}
	if wasInitialized {
		if err := usbClose(); err != nil {
			log.Printf("警告: USB关闭失败: %v", err)
		}
	}
	if c.rxChan != nil {
		close(c.rxChan)
		c.rxChan = nil
	}
	if c.ownsDevice {
		releaseToomossSession()
		c.ownsDevice = false
	}
}

func (c *Toomoss) readLoop() {
	ticker := time.NewTicker(c.cfg.PollingInterval)
	defer ticker.Stop()
	var canMsg [MsgBufferSize]CanMsg
	var canFDMsg [MsgBufferSize]CanfdMsg
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			if c.legacyCAN {
				c.readClassicBurst(&canMsg)
				continue
			}
			getCanFDMsgNum, _, _ := syscall.SyscallN(
				CANFDGetMsg,
				uintptr(DevHandle[DEVIndex]),
				uintptr(c.CANChannel),
				uintptr(unsafe.Pointer(&canFDMsg[0])),
				uintptr(len(canFDMsg)),
			)

			if getCanFDMsgNum <= 0 {
				continue
			}

			for i := 0; i < int(getCanFDMsgNum); i++ {
				msg := canFDMsg[i]
				if msg.ID > toomossCANFDIDMaskStandard {
					continue
				}
				isFD := msg.Flags&CANFD_MSG_FLAG_FDF != 0
				actualLen := toomossDLCToDataLen(msg.DLC, isFD)
				normalizedDLC := dataLenToDlc(actualLen)
				unifiedMsg := CanFrame{
					Direction: RX, ID: msg.ID, DLC: normalizedDLC, Data: msg.Data, IsFD: isFD,
				}

				msgType := c.canType
				if msg.Flags == CAN_MSG_FLAG_STD {
					msgType = CAN
				} else {
					msgType = CANFD
				}
				logCANMessage("RX", unifiedMsg.ID, unifiedMsg.DLC, unifiedMsg.Data[:actualLen], msgType)

				select {
				case c.rxChan <- unifiedMsg:
				default:
					log.Println("警告: 驱动接收channel(FD)已满，消息被丢弃")
				}
			}
		}
	}
}

func (c *Toomoss) readClassicBurst(canMsg *[MsgBufferSize]CanMsg) {
	if CANGetMsg == 0 {
		log.Println("CAN_GetMsg not loaded")
		return
	}

	getCANMsgNum, _, _ := syscall.SyscallN(
		CANGetMsg,
		uintptr(DevHandle[DEVIndex]),
		uintptr(c.CANChannel),
		uintptr(unsafe.Pointer(&canMsg[0])),
	)
	if getCANMsgNum <= 0 {
		return
	}

	for i := 0; i < int(getCANMsgNum); i++ {
		msg := canMsg[i]
		_, remote, extended, errorFrame, txEcho := decodeToomossClassicFlags(msg.RemoteFlag, msg.ExternFlag)
		if errorFrame {
			continue
		}
		if extended {
			continue
		}
		if remote {
			continue
		}
		direction := RX
		if txEcho {
			if !c.cfg.IncludeTxEcho {
				continue
			}
			direction = TX
		}
		actualLen := int(msg.DataLen)
		if actualLen > len(msg.Data) {
			actualLen = len(msg.Data)
		}
		id := uint32(msg.ID) & toomossCANFDIDMaskStandard

		var data [64]byte
		if actualLen > 0 {
			copy(data[:], msg.Data[:actualLen])
		}
		unifiedMsg := CanFrame{
			Direction: direction,
			ID:        id,
			DLC:       dataLenToDlc(actualLen),
			Data:      data,
			IsFD:      false,
		}

		logCANMessage("RX", unifiedMsg.ID, unifiedMsg.DLC, unifiedMsg.Data[:actualLen], CAN)
		select {
		case c.rxChan <- unifiedMsg:
		default:
			log.Println("Warning: CAN receive channel is full, dropping message")
		}
	}
}

func (c *Toomoss) drainInitialBuffer() {
	if c.legacyCAN {
		var canMsg [MsgBufferSize]CanMsg
		for batch := 0; batch < 16; batch++ {
			n, _, _ := syscall.SyscallN(
				CANGetMsg,
				uintptr(DevHandle[DEVIndex]),
				uintptr(c.CANChannel),
				uintptr(unsafe.Pointer(&canMsg[0])),
			)
			if int(n) <= 0 {
				return
			}
		}
		log.Println("Toomoss initial classic CAN queue still contains frames after 16 batches")
		return
	}

	var canFDMsg [MsgBufferSize]CanfdMsg
	for batch := 0; batch < 16; batch++ {
		n, _, _ := syscall.SyscallN(
			CANFDGetMsg,
			uintptr(DevHandle[DEVIndex]),
			uintptr(c.CANChannel),
			uintptr(unsafe.Pointer(&canFDMsg[0])),
			uintptr(len(canFDMsg)),
		)
		if int(n) <= 0 {
			return
		}
	}
	log.Println("Toomoss initial CAN-FD queue still contains frames after 16 batches")
}

func (c *Toomoss) Write(id int32, fd bool, data []byte) error {
	c.lifecycle.opMu.Lock()
	defer c.lifecycle.opMu.Unlock()
	if !c.lifecycle.isInitialized() {
		return errors.New("Toomoss driver is not initialized")
	}
	if err := validateWrite(c.cfg, id, fd, data); err != nil {
		return err
	}
	if c.legacyCAN {
		return c.writeClassicCAN(id, fd, data)
	}

	var canFDMsg [1]CanfdMsg
	var tempData [64]byte
	copy(tempData[:], data)
	canFDMsg[0].ID = uint32(id)
	switch {
	case !fd:
		canFDMsg[0].Flags = 0
	case fd:
		canFDMsg[0].Flags = CANFD_MSG_FLAG_FDF
	default:
		canFDMsg[0].Flags = CANFD_MSG_FLAG_FDF
	}

	canFDMsg[0].DLC = byte(len(data))
	canFDMsg[0].Data = tempData

	sendRet, _, _ := syscall.SyscallN(
		CANFDSendMsg,
		uintptr(DevHandle[DEVIndex]),
		uintptr(c.CANChannel),
		uintptr(unsafe.Pointer(&canFDMsg[0])),
		uintptr(len(canFDMsg)),
	)

	if int(sendRet) == len(canFDMsg) {
		logType := CAN
		if fd {
			logType = CANFD
		}

		normalizedDLC := dataLenToDlc(len(data))
		unifiedMsg := CanFrame{
			Direction: TX, ID: canFDMsg[0].ID, DLC: normalizedDLC, Data: canFDMsg[0].Data, IsFD: canFDMsg[0].Flags&CANFD_MSG_FLAG_FDF != 0,
		}

		logCANMessage("TX", uint32(id), normalizedDLC, canFDMsg[0].Data[:len(data)], logType)
		if c.cfg.IncludeTxEcho {
			select {
			case c.rxChan <- unifiedMsg:
			default:
				log.Println("警告: 驱动接收channel(FD)已满，消息被丢弃")
			}
		}
	} else {
		log.Printf("错误: CAN/CANFD消息发送失败, ID=0x%03X", id)
		return errors.New("CAN/CANFD消息发送失败")
	}
	return nil
}

func (c *Toomoss) writeClassicCAN(id int32, fd bool, data []byte) error {
	if CANSendMsg == 0 {
		return errors.New("CAN_SendMsg not loaded")
	}
	if fd {
		return errors.New("legacy Toomoss firmware does not support CAN-FD frames")
	}
	if len(data) > 8 {
		return fmt.Errorf("data length %d exceeds CAN maximum length 8", len(data))
	}

	var canMsg CanMsg
	copy(canMsg.Data[:], data)
	canID := uint32(id) & toomossCANFDIDMaskStandard
	canMsg.ID = int32(canID)
	remoteFlag, externFlag := encodeToomossClassicFlags(c.CANChannel, false, false)
	canMsg.RemoteFlag = remoteFlag
	canMsg.ExternFlag = externFlag
	canMsg.DataLen = byte(len(data))

	sendRet, _, _ := syscall.SyscallN(
		CANSendMsg,
		uintptr(DevHandle[DEVIndex]),
		uintptr(c.CANChannel),
		uintptr(unsafe.Pointer(&canMsg)),
		uintptr(1),
	)
	if int(sendRet) != 1 {
		log.Printf("error: CAN message send failed, ID=0x%03X", canID)
		return errors.New("CAN message send failed")
	}

	var unifiedData [64]byte
	copy(unifiedData[:], data)
	unifiedMsg := CanFrame{
		Direction: TX,
		ID:        canID,
		DLC:       dataLenToDlc(len(data)),
		Data:      unifiedData,
		IsFD:      false,
	}
	logCANMessage("TX", canID, unifiedMsg.DLC, data, CAN)
	if c.cfg.IncludeTxEcho {
		select {
		case c.rxChan <- unifiedMsg:
		default:
			log.Println("Warning: CAN receive channel is full, dropping TX echo")
		}
	}
	return nil
}

func (c *Toomoss) RxChan() <-chan CanFrame {
	c.lifecycle.opMu.Lock()
	defer c.lifecycle.opMu.Unlock()
	if c.fanout == nil {
		return nil
	}
	return c.fanout.Subscribe(c.cfg.RxBufferSize)
}

func (c *Toomoss) IsFDMode() bool {
	c.lifecycle.opMu.Lock()
	defer c.lifecycle.opMu.Unlock()
	return c.canType == CANFD
}

func (c *Toomoss) Config() Config {
	c.lifecycle.opMu.Lock()
	defer c.lifecycle.opMu.Unlock()
	return c.cfg
}
