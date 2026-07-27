//go:build windows

package driver

import (
	"errors"
	"fmt"
	"log"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"github.com/LoveWonYoung/atlas/liniface"
	"github.com/LoveWonYoung/atlas/tplin"
)

const (
	LIN_EX_SUCCESS = -iota
	LIN_EX_ERR_NOT_SUPPORT
	LIN_EX_ERR_USB_WRITE_FAIL
	LIN_EX_ERR_USB_READ_FAIL
	LIN_EX_ERR_CMD_FAIL
	LIN_EX_ERR_CH_NO_INIT
	LIN_EX_ERR_READ_DATA
	LIN_EX_ERR_PARAMETER
)

const (
	LIN_EX_MSG_TYPE_UN = iota
	LIN_EX_MSG_TYPE_MW
	LIN_EX_MSG_TYPE_MR
	LIN_EX_MSG_TYPE_SW
	LIN_EX_MSG_TYPE_SR
	LIN_EX_MSG_TYPE_BK
	LIN_EX_MSG_TYPE_SY
	LIN_EX_MSG_TYPE_ID
	LIN_EX_MSG_TYPE_DT
	LIN_EX_MSG_TYPE_CK
	LIN_EX_CHECK_STD   = iota - 10 // 标准校验，不含PID
	LIN_EX_CHECK_EXT               // 增强校验，含PID
	LIN_EX_CHECK_USER              // 自定义校验类型，需要用户自行计算并传入Check，不进行自动校验
	LIN_EX_CHECK_NONE              // 不进行校验数据
	LIN_EX_CHECK_ERROR             // 接收数据校验错误
)

var (
	toomossInstanceMu     sync.Mutex
	toomossInstanceActive bool
)

type LinExMsg struct {
	Timestamp uint32
	MsgType   uint8
	CheckType uint8
	DataLen   uint8
	Sync      uint8
	PID       uint8
	Data      [8]uint8
	Check     uint8
	BreakBits uint8
	Reserve1  uint8
}

var (
	Bt     uint = 19200
	Master byte = 1
)

type ToomossLIN struct {
	callMu     sync.Mutex
	stateMu    sync.RWMutex
	closeOnce  sync.Once
	closed     bool
	channels   map[liniface.Channel]struct{}
	eventMu    sync.Mutex
	eventChans map[liniface.Channel]chan *liniface.LinEvent
}

func logLINMessage(direction string, id byte, len_ byte, cs byte, data []byte) {
	if !printLogEnabled() {
		return
	}
	format := "%s LIN: ID=0x%02X, Len=%02d, CS=%02X, Data=% 02X"
	log.Printf(format, direction, id, len_, cs, data)
}

func NewToomossLIN(channel []byte, mode byte) (*ToomossLIN, error) {
	toomossInstanceMu.Lock()
	defer toomossInstanceMu.Unlock()
	if toomossInstanceActive {
		return nil, errors.New("a Toomoss device instance is already active; configure all LIN channels on that instance")
	}
	if len(channel) == 0 {
		return nil, errors.New("at least one LIN channel is required")
	}
	if err := ensureLinReady(); err != nil {
		return nil, err
	}
	// CAN 的 Init 也会 UsbScan/UsbOpen；已打开则复用 handle，避免重复打开报错。
	openedHere := false
	if !isToomossUSBOpened() {
		if ok := UsbScan(); !ok {
			return nil, fmt.Errorf("USB scan failed: device not found or DLL missing")
		}
		if ok := UsbOpen(); !ok {
			return nil, fmt.Errorf("USB open failed")
		}
		openedHere = true
	}
	for _, ch := range channel {
		if tmsInit, ret, err := syscall.SyscallN(LinExInit, uintptr(DevHandle[DEVIndex]), uintptr(ch), uintptr(Bt), uintptr(mode)); tmsInit != 0 {
			if openedHere {
				_ = usbClose()
			}
			return nil, fmt.Errorf("failed to initialize Toomoss LIN device: ret=%d, err=%v", ret, err)
		}
	}
	log.Println("Toomoss LIN device initialized successfully.")

	initializedChannels := make(map[liniface.Channel]struct{}, len(channel))
	for _, ch := range channel {
		initializedChannels[liniface.Channel(ch)] = struct{}{}
	}
	toomossInstanceActive = true
	return &ToomossLIN{
		channels:   initializedChannels,
		eventChans: make(map[liniface.Channel]chan *liniface.LinEvent),
	}, nil
}

func (d *ToomossLIN) LinMasterSync(msg, outMsg []LinExMsg, channel byte) (uintptr, error) {
	if len(outMsg) == 0 || len(msg) == 0 {
		return 0, fmt.Errorf("LinMasterSync called with empty outMsg")
	}
	if len(msg) != len(outMsg) {
		return 0, fmt.Errorf("LinMasterSync: len(msg) != len(outMsg)")
	}
	d.callMu.Lock()
	defer d.callMu.Unlock()
	if err := d.validateChannel(liniface.Channel(channel)); err != nil {
		return 0, err
	}
	ret, _, err := syscall.SyscallN(
		LinExMasterSync,
		uintptr(DevHandle[DEVIndex]),
		uintptr(channel),
		uintptr(unsafe.Pointer(&msg[0])),
		uintptr(unsafe.Pointer(&outMsg[0])),
		uintptr(len(msg)),
	)
	return ret, err
}

func (d *ToomossLIN) ReadEvent(timeout time.Duration, channel liniface.Channel) (*liniface.LinEvent, error) {
	if err := d.validateChannel(channel); err != nil {
		return nil, err
	}
	eventChan := d.eventChannel(channel)
	deadline := time.Now().Add(timeout)
	for {
		select {
		case event := <-eventChan:
			return event, nil
		default:
		}
		messages, err := d.LinExSlaveGetData(byte(channel))
		if err != nil {
			return nil, err
		}
		if len(messages) > 0 {
			for i := 1; i < len(messages); i++ {
				select {
				case eventChan <- toomossEvent(messages[i], channel):
				default:
				}
			}
			return toomossEvent(messages[0], channel), nil
		}
		if timeout <= 0 || !time.Now().Before(deadline) {
			return nil, nil
		}
		time.Sleep(time.Millisecond)
	}
}

func toomossEvent(message LinExMsg, channel liniface.Channel) *liniface.LinEvent {
	dataLen := min(int(message.DataLen), len(message.Data))
	payload := append([]byte(nil), message.Data[:dataLen]...)
	direction := liniface.RX
	if message.MsgType == LIN_EX_MSG_TYPE_SW {
		direction = liniface.TX
	}
	checksumType := liniface.EnhancedChecksum
	if message.CheckType == LIN_EX_CHECK_STD {
		checksumType = liniface.ClassicChecksum
	}
	return &liniface.LinEvent{
		Channel:      channel,
		EventID:      message.PID & 0x3F,
		EventPayload: payload,
		ChecksumType: checksumType,
		Direction:    direction,
		Timestamp:    time.Now(),
	}
}

func (d *ToomossLIN) WriteMessage(event *liniface.LinEvent, channel liniface.Channel) error {
	if event == nil {
		return errors.New("nil LIN event")
	}
	if len(event.EventPayload) > 8 {
		return fmt.Errorf("invalid LIN payload length %d (max 8)", len(event.EventPayload))
	}
	msg := make([]LinExMsg, 1)
	outMsg := make([]LinExMsg, 1)
	var payload [8]byte
	copy(payload[:], event.EventPayload)

	msg[0].MsgType = LIN_EX_MSG_TYPE_MW
	msg[0].DataLen = uint8(len(event.EventPayload))
	msg[0].PID = event.EventID
	msg[0].Data = payload
	if event.EventID == tplin.MasterDiagnosticFrameID || event.EventID == tplin.SlaveDiagnosticFrameID {
		msg[0].CheckType = LIN_EX_CHECK_STD
	} else {
		msg[0].CheckType = LIN_EX_CHECK_EXT
	}

	ret, err := d.LinMasterSync(msg, outMsg, byte(channel))
	if ret <= 0 {
		return fmt.Errorf("toomoss LIN write failed: ret=%d, err=%v", ret, err)
	}
	logLINMessage("TX", event.EventID, outMsg[0].DataLen, outMsg[0].Check, payload[:outMsg[0].DataLen])
	txEvent := *event
	txEvent.Channel = channel
	txEvent.Direction = liniface.TX
	txEvent.Timestamp = time.Now()

	select {
	case d.eventChannel(channel) <- &txEvent:
	default:
	}
	return nil
}
func (d *ToomossLIN) MasterWrite(frameID byte, data []byte, channel byte) error {
	if len(data) > 8 {
		return fmt.Errorf("toomoss MasterWrite: data length %d exceeds 8", len(data))
	}

	msg := make([]LinExMsg, 1)
	outMsg := make([]LinExMsg, 1)
	var payload [8]byte
	copy(payload[:], data)

	msg[0].MsgType = LIN_EX_MSG_TYPE_MW
	msg[0].DataLen = uint8(len(data))
	msg[0].PID = frameID
	msg[0].Data = payload
	if frameID == tplin.MasterDiagnosticFrameID || frameID == tplin.SlaveDiagnosticFrameID {
		msg[0].CheckType = LIN_EX_CHECK_STD
	} else {
		msg[0].CheckType = LIN_EX_CHECK_EXT
	}
	ret, err := d.LinMasterSync(msg, outMsg, channel)

	if ret <= 0 {
		return fmt.Errorf("toomoss LIN write failed: ret=%d, err=%v", ret, err)
	}
	logLINMessage("TX", frameID, outMsg[0].DataLen, outMsg[0].Check, payload[:outMsg[0].DataLen])
	return nil
}

func (d *ToomossLIN) MasterRead(frameID byte, channel byte) ([]byte, error) {
	msg := make([]LinExMsg, 1)
	outMsg := make([]LinExMsg, 1)
	msg[0].MsgType = LIN_EX_MSG_TYPE_MR
	msg[0].PID = frameID
	ret, _ := d.LinMasterSync(msg, outMsg, channel)

	if ret <= 0 {
		return nil, errors.New("no response from slave")
	}

	dataLen := int(outMsg[0].DataLen)
	if dataLen > len(outMsg[0].Data) {
		dataLen = len(outMsg[0].Data)
	}
	result := make([]byte, dataLen)
	copy(result, outMsg[0].Data[:dataLen])
	logLINMessage("RX", frameID, byte(dataLen), outMsg[0].Check, outMsg[0].Data[:dataLen])
	return result, nil
}

func (d *ToomossLIN) RequestSlaveResponse(frameID byte, channel liniface.Channel) error {
	msg := make([]LinExMsg, 1)
	outMsg := make([]LinExMsg, 1)
	msg[0].MsgType = LIN_EX_MSG_TYPE_MR
	msg[0].PID = frameID
	ret, _ := d.LinMasterSync(msg, outMsg, byte(channel))

	if ret <= 0 {
		log.Printf("RX : 0x%02X (No response from slave)", frameID)
		return nil
	}

	responseData := outMsg[0].Data
	dataLen := outMsg[0].DataLen
	if int(dataLen) > len(responseData) {
		dataLen = byte(len(responseData))
	}
	if dataLen == 0 {
		log.Printf("RX : 0x%02X (No response from slave)", frameID)
		return nil
	}
	if ret == 1 {
		logLINMessage("RX", frameID, dataLen, outMsg[0].Check, responseData[:dataLen])
	}

	rxEvent := &liniface.LinEvent{
		Channel:      channel,
		EventID:      frameID,
		EventPayload: responseData[:dataLen],
		Direction:    liniface.RX,
		Timestamp:    time.Now(),
	}

	select {
	case d.eventChannel(channel) <- rxEvent:
	default:
		return errors.New("toomoss event channel is full, discarding slave response")
	}
	return nil
}

func (d *ToomossLIN) ScheduleSlaveResponse(event *liniface.LinEvent, channel liniface.Channel) error {
	return errors.New("toomoss: ScheduleSlaveResponse is not supported in Master mode")
}

func (d *ToomossLIN) eventChannel(channel liniface.Channel) chan *liniface.LinEvent {
	d.eventMu.Lock()
	defer d.eventMu.Unlock()
	eventChan := d.eventChans[channel]
	if eventChan == nil {
		eventChan = make(chan *liniface.LinEvent, 10)
		d.eventChans[channel] = eventChan
	}
	return eventChan
}

// Close releases the USB adapter and loaded driver library.
func (d *ToomossLIN) Close() error {
	if d == nil {
		return nil
	}
	var closeErr error
	d.closeOnce.Do(func() {
		d.stateMu.Lock()
		d.closed = true
		d.stateMu.Unlock()
		d.callMu.Lock()
		closeErr = usbClose()
		d.callMu.Unlock()
		toomossInstanceMu.Lock()
		toomossInstanceActive = false
		toomossInstanceMu.Unlock()
	})
	return closeErr
}

func (d *ToomossLIN) LinBreak(channel byte) error {
	msg := make([]LinExMsg, 1)
	outMsg := make([]LinExMsg, 1)
	msg[0].MsgType = LIN_EX_MSG_TYPE_BK
	msg[0].Timestamp = 20
	if ret, _ := d.LinMasterSync(msg, outMsg, channel); ret <= 0 {
		return errors.New("LIN break failed")
	}
	return nil
}

const linExSlaveGetDataMaxFrames = 512

func (d *ToomossLIN) LinExSlaveGetData(channel byte) ([]LinExMsg, error) {
	if LinEXSlaveGetData == 0 {
		return nil, errors.New("LIN_EX_SlaveGetData not loaded")
	}

	linMsgs := make([]LinExMsg, linExSlaveGetDataMaxFrames)
	d.callMu.Lock()
	if err := d.validateChannel(liniface.Channel(channel)); err != nil {
		d.callMu.Unlock()
		return nil, err
	}
	ret, _, callErr := syscall.SyscallN(
		LinEXSlaveGetData,
		uintptr(DevHandle[DEVIndex]),
		uintptr(channel),
		uintptr(unsafe.Pointer(&linMsgs[0])),
	)
	d.callMu.Unlock()
	if callErr != 0 {
		return nil, fmt.Errorf("LIN_EX_SlaveGetData syscall failed: %w", callErr)
	}
	if int(ret) < 0 {
		return nil, fmt.Errorf("LIN_EX_SlaveGetData failed: ret=%d", int(ret))
	}

	count := int(ret)
	if count > len(linMsgs) {
		count = len(linMsgs)
	}
	return linMsgs[:count], nil
}

func (d *ToomossLIN) validateChannel(channel liniface.Channel) error {
	if d == nil {
		return liniface.ErrDriverClosed
	}
	d.stateMu.RLock()
	defer d.stateMu.RUnlock()
	if d.closed {
		return liniface.ErrDriverClosed
	}
	if _, ok := d.channels[channel]; !ok {
		return fmt.Errorf("%w: %d", liniface.ErrInvalidChannel, channel)
	}
	return nil
}
