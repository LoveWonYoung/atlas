package uds_client

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/LoveWonYoung/atlas/can_driver"
	isotp "github.com/LoveWonYoung/atlas/tp_layer"
)

// Transport 定义了 UDS 客户端所需的 ISO-TP 传输层接口
// 这允许我们在测试中注入 Mock 对象
type Transport interface {
	Send(data []byte)
	RecvChan() <-chan []byte
	SetTxAddress(addr *isotp.Address)
	SetFDMode(isFD bool)
	Run(ctx context.Context, rxChan <-chan isotp.CanMessage, txChan chan<- isotp.CanMessage)
}

// 通道缓冲区大小常量
const (
	driverRxBufferSize     = 100                     // 驱动接收缓冲区大小
	driverTxBufferSize     = 1024                    // 驱动发送缓冲区（大块请求 + STmin=0 时 CF 突发，适当加大）
	responsePendingTimeout = 5000 * time.Millisecond // Response Pending 超时
	defaultOverallTimeout  = 30 * time.Second
	defaultMaxRetries      = 3 // 默认最大重试次数
)

// UDS 负响应码 (Negative Response Code)
const (
	PositiveResponse                                  = 0x00
	GeneralReject                                     = 0x10
	ServiceNotSupported                               = 0x11
	SubFunctionNotSupported                           = 0x12
	IncorrectMessageLengthOrInvalidFormat             = 0x13
	ResponseTooLong                                   = 0x14
	BusyRepeatRequest                                 = 0x21
	ConditionsNotCorrect                              = 0x22
	RequestSequenceError                              = 0x24
	NoResponseFromSubnetComponent                     = 0x25
	FailurePreventsExecutionOfRequestedAction         = 0x26
	RequestOutOfRange                                 = 0x31
	SecurityAccessDenied                              = 0x33
	AuthenticationRequired                            = 0x34
	InvalidKey                                        = 0x35
	ExceedNumberOfAttempts                            = 0x36
	RequiredTimeDelayNotExpired                       = 0x37
	SecureDataTransmissionRequired                    = 0x38
	SecureDataTransmissionNotAllowed                  = 0x39
	SecureDataVerificationFailed                      = 0x3A
	CertificateVerificationFailed_InvalidTimePeriod   = 0x50
	CertificateVerificationFailed_InvalidSignature    = 0x51
	CertificateVerificationFailed_InvalidChainOfTrust = 0x52
	CertificateVerificationFailed_InvalidType         = 0x53
	CertificateVerificationFailed_InvalidFormat       = 0x54
	CertificateVerificationFailed_InvalidContent      = 0x55
	CertificateVerificationFailed_InvalidScope        = 0x56
	CertificateVerificationFailed_InvalidCertificate  = 0x57
	OwnershipVerificationFailed                       = 0x58
	ChallengeCalculationFailed                        = 0x59
	SettingAccessRightsFailed                         = 0x5A
	SessionKeyCreationDerivationFailed                = 0x5B
	ConfigurationDataUsageFailed                      = 0x5C
	DeAuthenticationFailed                            = 0x5D
	UploadDownloadNotAccepted                         = 0x70
	TransferDataSuspended                             = 0x71
	GeneralProgrammingFailure                         = 0x72
	WrongBlockSequenceCounter                         = 0x73
	RequestCorrectlyReceived_ResponsePending          = 0x78
	SubFunctionNotSupportedInActiveSession            = 0x7E
	ServiceNotSupportedInActiveSession                = 0x7F
	RpmTooHigh                                        = 0x81
	RpmTooLow                                         = 0x82
	EngineIsRunning                                   = 0x83
	EngineIsNotRunning                                = 0x84
	EngineRunTimeTooLow                               = 0x85
	TemperatureTooHigh                                = 0x86
	TemperatureTooLow                                 = 0x87
	VehicleSpeedTooHigh                               = 0x88
	VehicleSpeedTooLow                                = 0x89
	ThrottlePedalTooHigh                              = 0x8A
	ThrottlePedalTooLow                               = 0x8B
	TransmissionRangeNotInNeutral                     = 0x8C
	TransmissionRangeNotInGear                        = 0x8D
	BrakeSwitchNotClosed                              = 0x8F
	ShifterLeverNotInPark                             = 0x90
	TorqueConverterClutchLocked                       = 0x91
	VoltageTooHigh                                    = 0x92
	VoltageTooLow                                     = 0x93
	ResourceTemporarilyNotAvailable                   = 0x94
	TerminationWithSignatureRequested                 = 0x3B
	AccessDenied                                      = 0x3C
	VersionNotSupported                               = 0x3D
	SecuredLinkNotSupported                           = 0x3E
	CertificateNotAvailable                           = 0x3F
	AuditTrailInformationNotAvailable                 = 0x40
)

type UDSError struct {
	ServiceID byte   // 原始服务 ID
	NRC       byte   // 负响应码
	Message   string // 错误描述
}

func (e *UDSError) Error() string {
	return fmt.Sprintf("UDS 负响应: SID=0x%02X, NRC=0x%02X (%s)", e.ServiceID, e.NRC, e.Message)
}

// IsRetryable 判断该错误是否可以重试
func (e *UDSError) IsRetryable() bool {
	switch e.NRC {
	case BusyRepeatRequest, RequestCorrectlyReceived_ResponsePending:
		return true
	default:
		return false
	}
}

// RequestOptions 请求配置选项
type RequestOptions struct {
	Timeout                time.Duration // 首次响应超时
	MaxRetries             int           // 最大重试次数 (仅对可重试错误生效)
	RetryDelay             time.Duration // 重试间隔
	ResponsePendingTimeout time.Duration // 收到 NRC 0x78 后等待下一响应的超时
	OverallTimeout         time.Duration // 包含重试和 Response Pending 的总体超时
}

// AddressingMode 控制发送请求时使用物理/功能寻址。
type AddressingMode int

const (
	AddressPhysical AddressingMode = iota
	AddressFunctional
)

// hasSubFunctionSuppressPositive 判断该服务是否有首字节子功能，且允许使用 bit7 抑制正响应
func hasSubFunctionSuppressPositive(sid byte) bool {
	switch sid {
	case 0x10, 0x11, 0x19, 0x27, 0x28, 0x2F, 0x31, 0x3E, 0x85, 0x86, 0x87:
		return true
	default:
		return false
	}
}

// DefaultRequestOptions 返回默认请求选项
func DefaultRequestOptions() RequestOptions {
	return RequestOptions{
		Timeout:                500 * time.Millisecond,
		MaxRetries:             defaultMaxRetries,
		RetryDelay:             100 * time.Millisecond,
		ResponsePendingTimeout: responsePendingTimeout,
		OverallTimeout:         defaultOverallTimeout,
	}
}

func normalizeRequestOptions(opts RequestOptions) (RequestOptions, error) {
	if opts.Timeout <= 0 {
		return RequestOptions{}, errors.New("请求超时必须大于 0")
	}
	if opts.MaxRetries < 0 {
		return RequestOptions{}, errors.New("最大重试次数不能为负数")
	}
	if opts.RetryDelay < 0 {
		return RequestOptions{}, errors.New("重试间隔不能为负数")
	}
	if opts.ResponsePendingTimeout < 0 {
		return RequestOptions{}, errors.New("Response Pending 超时不能为负数")
	}
	if opts.OverallTimeout < 0 {
		return RequestOptions{}, errors.New("总体请求超时不能为负数")
	}
	if opts.ResponsePendingTimeout == 0 {
		opts.ResponsePendingTimeout = responsePendingTimeout
	}
	if opts.OverallTimeout == 0 {
		opts.OverallTimeout = defaultOverallTimeout
		if opts.Timeout > opts.OverallTimeout {
			opts.OverallTimeout = opts.Timeout
		}
	}
	return opts, nil
}

// nrcDescriptions 缓存 NRC 错误描述，避免重复创建 map
var nrcDescriptions = map[byte]string{
	PositiveResponse:                                  "PositiveResponse",
	GeneralReject:                                     "GeneralReject",
	ServiceNotSupported:                               "ServiceNotSupported",
	SubFunctionNotSupported:                           "SubFunctionNotSupported",
	IncorrectMessageLengthOrInvalidFormat:             "IncorrectMessageLengthOrInvalidFormat",
	ResponseTooLong:                                   "ResponseTooLong",
	BusyRepeatRequest:                                 "BusyRepeatRequest",
	ConditionsNotCorrect:                              "ConditionsNotCorrect",
	RequestSequenceError:                              "RequestSequenceError",
	NoResponseFromSubnetComponent:                     "NoResponseFromSubnetComponent",
	FailurePreventsExecutionOfRequestedAction:         "FailurePreventsExecutionOfRequestedAction",
	RequestOutOfRange:                                 "RequestOutOfRange",
	SecurityAccessDenied:                              "SecurityAccessDenied",
	AuthenticationRequired:                            "AuthenticationRequired",
	InvalidKey:                                        "InvalidKey",
	ExceedNumberOfAttempts:                            "ExceedNumberOfAttempts",
	RequiredTimeDelayNotExpired:                       "RequiredTimeDelayNotExpired",
	SecureDataTransmissionRequired:                    "SecureDataTransmissionRequired",
	SecureDataTransmissionNotAllowed:                  "SecureDataTransmissionNotAllowed",
	SecureDataVerificationFailed:                      "SecureDataVerificationFailed",
	CertificateVerificationFailed_InvalidTimePeriod:   "CertificateVerificationFailed_InvalidTimePeriod",
	CertificateVerificationFailed_InvalidSignature:    "CertificateVerificationFailed_InvalidSignature",
	CertificateVerificationFailed_InvalidChainOfTrust: "CertificateVerificationFailed_InvalidChainOfTrust",
	CertificateVerificationFailed_InvalidType:         "CertificateVerificationFailed_InvalidType",
	CertificateVerificationFailed_InvalidFormat:       "CertificateVerificationFailed_InvalidFormat",
	CertificateVerificationFailed_InvalidContent:      "CertificateVerificationFailed_InvalidContent",
	CertificateVerificationFailed_InvalidScope:        "CertificateVerificationFailed_InvalidScope",
	CertificateVerificationFailed_InvalidCertificate:  "CertificateVerificationFailed_InvalidCertificate",
	OwnershipVerificationFailed:                       "OwnershipVerificationFailed",
	ChallengeCalculationFailed:                        "ChallengeCalculationFailed",
	SettingAccessRightsFailed:                         "SettingAccessRightsFailed",
	SessionKeyCreationDerivationFailed:                "SessionKeyCreationDerivationFailed",
	ConfigurationDataUsageFailed:                      "ConfigurationDataUsageFailed",
	DeAuthenticationFailed:                            "DeAuthenticationFailed",
	UploadDownloadNotAccepted:                         "UploadDownloadNotAccepted",
	TransferDataSuspended:                             "TransferDataSuspended",
	GeneralProgrammingFailure:                         "GeneralProgrammingFailure",
	WrongBlockSequenceCounter:                         "WrongBlockSequenceCounter",
	RequestCorrectlyReceived_ResponsePending:          "RequestCorrectlyReceived_ResponsePending",
	SubFunctionNotSupportedInActiveSession:            "SubFunctionNotSupportedInActiveSession",
	ServiceNotSupportedInActiveSession:                "ServiceNotSupportedInActiveSession",
	RpmTooHigh:                                        "RpmTooHigh",
	RpmTooLow:                                         "RpmTooLow",
	EngineIsRunning:                                   "EngineIsRunning",
	EngineIsNotRunning:                                "EngineIsNotRunning",
	EngineRunTimeTooLow:                               "EngineRunTimeTooLow",
	TemperatureTooHigh:                                "TemperatureTooHigh",
	TemperatureTooLow:                                 "TemperatureTooLow",
	VehicleSpeedTooHigh:                               "VehicleSpeedTooHigh",
	VehicleSpeedTooLow:                                "VehicleSpeedTooLow",
	ThrottlePedalTooHigh:                              "ThrottlePedalTooHigh",
	ThrottlePedalTooLow:                               "ThrottlePedalTooLow",
	TransmissionRangeNotInNeutral:                     "TransmissionRangeNotInNeutral",
	TransmissionRangeNotInGear:                        "TransmissionRangeNotInGear",
	BrakeSwitchNotClosed:                              "BrakeSwitchNotClosed",
	ShifterLeverNotInPark:                             "ShifterLeverNotInPark",
	TorqueConverterClutchLocked:                       "TorqueConverterClutchLocked",
	VoltageTooHigh:                                    "VoltageTooHigh",
	VoltageTooLow:                                     "VoltageTooLow",
	ResourceTemporarilyNotAvailable:                   "ResourceTemporarilyNotAvailable",
	TerminationWithSignatureRequested:                 "TerminationWithSignatureRequested",
	AccessDenied:                                      "AccessDenied",
	VersionNotSupported:                               "VersionNotSupported",
	SecuredLinkNotSupported:                           "SecuredLinkNotSupported",
	CertificateNotAvailable:                           "CertificateNotAvailable",
	AuditTrailInformationNotAvailable:                 "AuditTrailInformationNotAvailable",
}

// getNRCDescription 获取 NRC 错误描述
func getNRCDescription(nrc byte) string {
	if desc, ok := nrcDescriptions[nrc]; ok {
		return desc
	}
	return "未知错误"
}

// UDSClient 是一个高级客户端，封装了所有初始化和通信的复杂性
type UDSClient struct {
	stack       Transport // 使用接口而非具体结构体
	driver      can_driver.CANDriver
	cancel      context.CancelFunc // 用于控制所有后台goroutine的生命周期
	ctx         context.Context    // 客户端生命周期 context
	txErrChan   chan error
	mode        AddressingMode
	funcAddr    *isotp.Address
	wg          sync.WaitGroup
	closeOnce   sync.Once
	gateOnce    sync.Once
	requestGate chan struct{}
	unsubscribe func()
}

// NewUDSClient 是新的构造函数，负责完成所有组件的初始化和连接。
func NewUDSClient(dev can_driver.CANDriver, addr *isotp.Address, cfg isotp.Config) (*UDSClient, error) {
	if err := addr.Validate(); err != nil {
		return nil, fmt.Errorf("invalid ISO-TP address: %w", err)
	}
	if dev == nil {
		return nil, errors.New("CAN can_driver instance cannot be nil")
	}
	if err := dev.Init(); err != nil {
		return nil, fmt.Errorf("failed to initialize CAN device: %w", err)
	}
	if starter, ok := dev.(can_driver.ErrorStartingCANDriver); ok {
		if err := starter.StartWithError(); err != nil {
			dev.Stop()
			return nil, fmt.Errorf("failed to start CAN device: %w", err)
		}
	} else {
		dev.Start()
	}

	stack := isotp.NewTransport(addr, cfg)
	stack.SetFDMode(dev.IsFDMode())

	return newUDSClient(dev, stack), nil
}

// newUDSClient 内部构造函数，支持依赖注入
func newUDSClient(dev can_driver.CANDriver, stack Transport) *UDSClient {
	ctx, cancel := context.WithCancel(context.Background())
	rxFromDriver := make(chan isotp.CanMessage, driverRxBufferSize)
	txToDriver := make(chan isotp.CanMessage, driverTxBufferSize)
	driverRx, unsubscribe := subscribeDriver(dev, driverRxBufferSize)
	c := &UDSClient{
		stack:       stack,
		driver:      dev,
		cancel:      cancel,
		ctx:         ctx,
		txErrChan:   make(chan error, 16),
		mode:        AddressPhysical,
		unsubscribe: unsubscribe,
	}

	c.run(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case raw, ok := <-driverRx:
				if !ok {
					return
				}
				msg, accepted := convertRXMessage(raw)
				if !accepted {
					continue
				}
				select {
				case <-ctx.Done():
					return
				case rxFromDriver <- msg:
				}
			}
		}
	})

	c.run(func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-txToDriver:
				if !ok {
					return
				}
				if err := dev.Write(int32(msg.ArbitrationID), msg.IsFD, msg.Data); err != nil {
					c.reportIOError(fmt.Errorf("failed to send CAN frame (id=0x%X): %w", msg.ArbitrationID, err))
				}
			}
		}
	})

	c.run(func() {
		stack.Run(ctx, rxFromDriver, txToDriver)
	})

	if s, ok := stack.(*isotp.Transport); ok {
		c.run(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case err, ok := <-s.ErrorChan:
					if !ok {
						return
					}
					c.reportIOError(fmt.Errorf("ISO-TP: %w", err))
				}
			}
		})
	}
	if observable, ok := dev.(can_driver.ObservableCANDriver); ok {
		driverErrors := observable.Errors()
		c.run(func() {
			for {
				select {
				case <-ctx.Done():
					return
				case err, ok := <-driverErrors:
					if !ok {
						return
					}
					c.reportIOError(fmt.Errorf("CAN driver: %w", err))
				}
			}
		})
	}
	return c
}

func subscribeDriver(dev can_driver.CANDriver, buffer int) (<-chan can_driver.CanFrame, func()) {
	if subscriber, ok := dev.(can_driver.RxSubscriber); ok {
		return subscriber.SubscribeRx(buffer)
	}
	return dev.RxChan(), func() {}
}

func (c *UDSClient) run(fn func()) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		fn()
	}()
}

func (c *UDSClient) reportIOError(err error) {
	if err == nil {
		return
	}
	select {
	case <-c.ctx.Done():
	case c.txErrChan <- err:
	default:
	}
}

// SetFunctionalAddress sets the functional address used when AddressFunctional is active.
func (c *UDSClient) SetFunctionalAddress(addr *isotp.Address) error {
	if err := addr.Validate(); err != nil {
		return fmt.Errorf("invalid functional address: %w", err)
	}
	if err := c.acquireRequest(context.Background()); err != nil {
		return err
	}
	defer c.releaseRequest()

	c.funcAddr = addr
	if c.mode == AddressFunctional {
		c.stack.SetTxAddress(addr)
	}
	return nil
}

// SetAddressingMode switches between physical and functional addressing for requests.
func (c *UDSClient) SetAddressingMode(mode AddressingMode) error {
	if err := c.acquireRequest(context.Background()); err != nil {
		return err
	}
	defer c.releaseRequest()

	if err := c.updateTxAddressLocked(mode); err != nil {
		return err
	}
	c.mode = mode
	return nil
}

// UseFunctionalAddress is a convenience wrapper for SetAddressingMode(AddressFunctional).
func (c *UDSClient) UseFunctionalAddress() error {
	return c.SetAddressingMode(AddressFunctional)
}

// UsePhysicalAddress is a convenience wrapper for SetAddressingMode(AddressPhysical).
func (c *UDSClient) UsePhysicalAddress() error {
	return c.SetAddressingMode(AddressPhysical)
}

func (c *UDSClient) updateTxAddressLocked(mode AddressingMode) error {
	switch mode {
	case AddressPhysical:
		c.stack.SetTxAddress(nil)
		return nil
	case AddressFunctional:
		if c.funcAddr == nil {
			return errors.New("functional address is not set")
		}
		c.stack.SetTxAddress(c.funcAddr)
		return nil
	default:
		return fmt.Errorf("unknown addressing mode: %d", mode)
	}
}

// SendAndRecv 发送一个请求并阻塞等待响应，内置超时处理。
func (c *UDSClient) SendAndRecv(payload []byte, timeout time.Duration) ([]byte, error) {
	return c.RequestWithContext(context.Background(), payload, RequestOptions{
		Timeout:    timeout,
		MaxRetries: 0, // 保持向后兼容，不重试
		RetryDelay: 0,
	})
}

// SendAndRecvWithAddressingMode sends one request with the specified addressing
// mode without changing the client's default addressing mode.
func (c *UDSClient) SendAndRecvWithAddressingMode(payload []byte, timeout time.Duration, mode AddressingMode) ([]byte, error) {
	return c.RequestWithContextAndAddressingMode(context.Background(), payload, RequestOptions{
		Timeout:    timeout,
		MaxRetries: 0, // 保持向后兼容，不重试
		RetryDelay: 0,
	}, mode)
}

// RequestWithContext 发送 UDS 请求并等待响应，支持 Context 取消。
func (c *UDSClient) RequestWithContext(ctx context.Context, payload []byte, opts RequestOptions) ([]byte, error) {
	return c.requestWithContext(ctx, payload, opts, nil)
}

// RequestWithContextAndAddressingMode sends one request with the specified
// addressing mode without changing the client's default addressing mode.
func (c *UDSClient) RequestWithContextAndAddressingMode(ctx context.Context, payload []byte, opts RequestOptions, mode AddressingMode) ([]byte, error) {
	return c.requestWithContext(ctx, payload, opts, &mode)
}

func (c *UDSClient) requestWithContext(ctx context.Context, payload []byte, opts RequestOptions, mode *AddressingMode) ([]byte, error) {
	if len(payload) == 0 {
		return nil, errors.New("请求 payload 不能为空")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	var err error
	opts, err = normalizeRequestOptions(opts)
	if err != nil {
		return nil, err
	}

	requestCtx, cancel := context.WithTimeout(ctx, opts.OverallTimeout)
	defer cancel()
	if err := c.acquireRequest(requestCtx); err != nil {
		return nil, err
	}
	defer c.releaseRequest()

	requestMode := c.mode
	if mode != nil {
		requestMode = *mode
	}
	if err := c.updateTxAddressLocked(requestMode); err != nil {
		return nil, err
	}

	requestSID := payload[0]
	expectedResponseSID := requestSID + 0x40                                                                      // 正响应 SID = 请求 SID + 0x40
	suppressPositive := hasSubFunctionSuppressPositive(requestSID) && len(payload) >= 2 && (payload[1]&0x80) != 0 // 仅对子功能服务识别 bit7

	var lastErr error
	var lastResp []byte
	for attempt := 0; attempt <= opts.MaxRetries; attempt++ {
		if attempt > 0 {
			if err := c.waitRetryDelay(requestCtx, opts.RetryDelay); err != nil {
				return nil, err
			}
		}

		response, err := c.singleRequest(requestCtx, payload, opts.Timeout, opts.ResponsePendingTimeout, suppressPositive)
		if err != nil {
			// 检查是否是 context 取消
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}

			// 检查是否是 UDS 错误
			var udsErr *UDSError
			if errors.As(err, &udsErr) {
				// 可重试的 UDS 错误 -> 记录最后一次错误和响应，然后重试
				if udsErr.IsRetryable() && attempt < opts.MaxRetries {
					lastErr = err
					lastResp = response
					continue
				}
				// 不可重试的 UDS 错误 -> 返回原始响应和错误
				return response, err
			}

			// 其他错误
			return nil, err
		}

		// 验证响应 SID
		if len(response) > 0 && response[0] != expectedResponseSID {
			// 检查是否是负响应
			if response[0] == 0x7F && len(response) >= 3 {
				return response, &UDSError{
					ServiceID: response[1],
					NRC:       response[2],
					Message:   getNRCDescription(response[2]),
				}
			}
			return response, fmt.Errorf("响应 SID 不匹配: 期望 0x%02X, 收到 0x%02X", expectedResponseSID, response[0])
		}

		return response, nil
	}

	if lastErr != nil {
		// 如果有最后一次响应，返回它以便调用方能查看原始帧
		return lastResp, fmt.Errorf("达到最大重试次数 (%d): %w", opts.MaxRetries, lastErr)
	}
	return nil, errors.New("未知错误")
}

func (c *UDSClient) acquireRequest(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.gateOnce.Do(func() {
		c.requestGate = make(chan struct{}, 1)
		c.requestGate <- struct{}{}
	})
	clientDone := (<-chan struct{})(nil)
	if c.ctx != nil {
		clientDone = c.ctx.Done()
		select {
		case <-clientDone:
			return errors.New("UDS 客户端已关闭")
		default:
		}
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-clientDone:
		return errors.New("UDS 客户端已关闭")
	case <-c.requestGate:
		return nil
	}
}

func (c *UDSClient) releaseRequest() {
	c.requestGate <- struct{}{}
}

func (c *UDSClient) waitRetryDelay(ctx context.Context, delay time.Duration) error {
	clientDone := (<-chan struct{})(nil)
	if c.ctx != nil {
		clientDone = c.ctx.Done()
	}
	if delay == 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-clientDone:
			return errors.New("UDS 客户端已关闭")
		default:
			return nil
		}
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-clientDone:
		return errors.New("UDS 客户端已关闭")
	case <-timer.C:
		return nil
	}
}

// singleRequest 执行单次请求（不含重试逻辑）
func (c *UDSClient) singleRequest(ctx context.Context, payload []byte, timeout, pendingTimeout time.Duration, suppressPositive bool) ([]byte, error) {
	c.drainStackRecv()
	c.drainTxErrors()

	if err := sendTransport(ctx, c.stack, payload); err != nil {
		return nil, err
	}

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	// 为防止测试时未初始化 c.ctx 导致空指针，使用本地 done channel
	clientDone := (<-chan struct{})(nil)
	if c.ctx != nil {
		clientDone = c.ctx.Done()
	}

	recvCh := c.stack.RecvChan()
	txErrCh := c.txErrChan

	for {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-clientDone:
			return nil, errors.New("UDS 客户端已关闭")
		case err := <-txErrCh:
			return nil, err
		case <-deadline.C:
			if suppressPositive {
				return nil, nil // 抑制正响应：超时视为成功完成
			}
			return nil, fmt.Errorf("等待响应超时 (%v)", timeout)
		case data, ok := <-recvCh:
			if !ok {
				return nil, errors.New("transport receive channel closed")
			}
			// 检查是否为负响应
			if len(data) >= 3 && data[0] == 0x7F {
				nrc := data[2]
				serviceSID := data[1]

				// Response Pending - 重置超时继续等待
				if serviceSID == payload[0] && nrc == RequestCorrectlyReceived_ResponsePending {
					if !deadline.Stop() {
						select {
						case <-deadline.C:
						default:
						}
					}
					deadline.Reset(pendingTimeout)
					continue
				}

				// 其他负响应
				return data, &UDSError{
					ServiceID: serviceSID,
					NRC:       nrc,
					Message:   getNRCDescription(nrc),
				}
			}
			return data, nil
		}
	}
}

type contextTransport interface {
	SendContext(context.Context, []byte) error
}

func sendTransport(ctx context.Context, stack Transport, payload []byte) error {
	if sender, ok := stack.(contextTransport); ok {
		return sender.SendContext(ctx, payload)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		stack.Send(payload)
		return nil
	}
}

func (c *UDSClient) drainStackRecv() {
	for {
		select {
		case _, ok := <-c.stack.RecvChan():
			if !ok {
				return
			}
		default:
			return
		}
	}
}

func (c *UDSClient) drainTxErrors() {
	if c.txErrChan == nil {
		return
	}
	for {
		select {
		case <-c.txErrChan:
		default:
			return
		}
	}
}

// Request 简化版请求函数，使用默认选项
func (c *UDSClient) Request(payload []byte) ([]byte, error) {
	return c.RequestWithContext(context.Background(), payload, DefaultRequestOptions())
}

// RequestWithTimeout 带自定义超时的请求函数
func (c *UDSClient) RequestWithTimeout(payload []byte, timeout time.Duration) ([]byte, error) {
	opts := DefaultRequestOptions()
	opts.Timeout = timeout
	return c.RequestWithContext(context.Background(), payload, opts)
}

// Close 优雅地关闭客户端，释放所有资源。
func (c *UDSClient) Close() {
	c.closeOnce.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}
		if c.unsubscribe != nil {
			c.unsubscribe()
		}
		if c.driver != nil {
			c.driver.Stop()
		}
		c.wg.Wait()
	})
}

// IsClosed 检查客户端是否已关闭
func (c *UDSClient) IsClosed() bool {
	if c.ctx == nil {
		return false
	}
	select {
	case <-c.ctx.Done():
		return true
	default:
		return false
	}
}
