package uds_client

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/LoveWonYoung/atlas/driver"
	isotp "github.com/LoveWonYoung/atlas/tp_layer"
)

// Transport defines the ISO-TP transport interface required by the UDS client.
// This allows injecting mock objects in tests.
type Transport interface {
	Send(data []byte)
	Recv() ([]byte, bool)
	RecvChan() <-chan []byte
	SetTxAddress(addr *isotp.Address)
	SetFDMode(isFD bool)
	Run(ctx context.Context, rxChan <-chan isotp.CanMessage, txChan chan<- isotp.CanMessage)
}

// Channel buffer size constants.
const (
	adapterRxBufferSize    = 100                     // Adapter receive buffer size
	adapterTxBufferSize    = 1024                    // Adapter transmit buffer (larger for bulk 0x36 + STmin=0 CF bursts)
	responsePendingTimeout = 5000 * time.Millisecond // Response Pending timeout
	defaultMaxRetries      = 3                       // Default max retry count
)

// UDS Negative Response Codes (NRC).
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
	ServiceID byte   // Original service ID
	NRC       byte   // Negative response code
	Message   string // Error description
}

func (e *UDSError) Error() string {
	return fmt.Sprintf("UDS negative response: SID=0x%02X, NRC=0x%02X (%s)", e.ServiceID, e.NRC, e.Message)
}

// IsRetryable reports whether this error is retryable.
func (e *UDSError) IsRetryable() bool {
	switch e.NRC {
	case BusyRepeatRequest, RequestCorrectlyReceived_ResponsePending:
		return true
	default:
		return false
	}
}

// RequestOptions configures a request.
type RequestOptions struct {
	Timeout    time.Duration // Per-request timeout
	MaxRetries int           // Max retries (only for retryable errors)
	RetryDelay time.Duration // Delay between retries
}

// AddressingMode selects physical or functional addressing for requests.
type AddressingMode int

const (
	AddressPhysical AddressingMode = iota
	AddressFunctional
)

// hasSubFunctionSuppressPositive reports whether the service has a first-byte
// sub-function and allows suppress-positive-response via bit7.
func hasSubFunctionSuppressPositive(sid byte) bool {
	switch sid {
	case 0x10, 0x11, 0x19, 0x27, 0x28, 0x2F, 0x31, 0x3E, 0x85, 0x86, 0x87:
		return true
	default:
		return false
	}
}

// DefaultRequestOptions returns the default request options.
func DefaultRequestOptions() RequestOptions {
	return RequestOptions{
		Timeout:    500 * time.Millisecond,
		MaxRetries: defaultMaxRetries,
		RetryDelay: 100 * time.Millisecond,
	}
}

// nrcDescriptions caches NRC descriptions to avoid repeated map creation.
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

// getNRCDescription returns the description for an NRC.
func getNRCDescription(nrc byte) string {
	if desc, ok := nrcDescriptions[nrc]; ok {
		return desc
	}
	return "Unknown error"
}

// UDSClient is a high-level client that encapsulates initialization and communication.
type UDSClient struct {
	stack     Transport // Interface instead of concrete type
	adapter   *driver.Adapter
	cancel    context.CancelFunc // Controls lifecycle of all background goroutines
	ctx       context.Context    // Client lifecycle context
	txErrChan chan error
	reqMu     sync.Mutex
	mode      AddressingMode
	funcAddr  *isotp.Address
}

// NewUDSClient constructs a client and initializes all components.
func NewUDSClient(dev driver.CANDriver, addr *isotp.Address, cfg isotp.Config) (*UDSClient, error) {
	// 1. Initialize adapter and start the hardware driver
	adapter, err := driver.NewAdapter(dev)
	if err != nil {
		return nil, fmt.Errorf("The adapter cannot be created.: %w", err)
	}

	// 2. Initialize the ISO-TP stack
	stack := isotp.NewTransport(addr, cfg)
	if isFD, ok := driver.DetectFDMode(dev); ok {
		stack.SetFDMode(isFD)
	}

	return newUDSClient(adapter, stack), nil
}

// newUDSClient is an internal constructor that supports dependency injection.
func newUDSClient(adapter *driver.Adapter, stack Transport) *UDSClient {
	// 3. Create a context for goroutine lifecycle management
	ctx, cancel := context.WithCancel(context.Background())
	txErrChan := make(chan error, 16)

	// 4. Create internal channels bridging the stack and adapter
	rxFromAdapter := make(chan isotp.CanMessage, adapterRxBufferSize)
	txToAdapter := make(chan isotp.CanMessage, adapterTxBufferSize)

	// 5. Start background goroutines (glue logic)
	// a. Receive from adapter and feed into the stack
	go func() {
		for {
			msg, ok := adapter.RxFuncWithContext(ctx)
			if !ok {
				return
			}
			select {
			case <-ctx.Done():
				return
			case rxFromAdapter <- msg:
			}
		}
	}()

	// b. Take pending TX data from the stack and send via adapter
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-txToAdapter:
				if !ok {
					return
				}
				if err := adapter.TxFunc(msg); err != nil {
					select {
					case <-ctx.Done():
						return
					case txErrChan <- err:
					default:
					}
				}
			}
		}
	}()

	// c. Drive the stack core state machine
	go func() {
		stack.Run(ctx, rxFromAdapter, txToAdapter)
	}()

	// d. Listen for stack errors (only when stack is the concrete type, or when
	// the interface is extended with ErrorChan). To keep the interface simple,
	// assume Run handles errors internally/externally; if the original
	// isotp.Transport must expose ErrorChan, add a getter to the interface or
	// type-assert here. The original code accessed stack.ErrorChan directly.
	// For simplicity, start an error listener when stack is *isotp.Transport.
	if s, ok := stack.(*isotp.Transport); ok {
		go func() {
			for {
				select {
				case <-ctx.Done():
					return
				case err := <-s.ErrorChan:
					log.Printf("[ISOTP Error] %v", err)
				}
			}
		}()
	}

	log.Println("UDS client initialized and started successfully.")
	return &UDSClient{
		stack:     stack,
		adapter:   adapter,
		cancel:    cancel,
		ctx:       ctx,
		txErrChan: txErrChan,
		mode:      AddressPhysical,
	}
}

// SetFunctionalAddress sets the functional address used when AddressFunctional is active.
func (c *UDSClient) SetFunctionalAddress(addr *isotp.Address) error {
	if addr == nil {
		return errors.New("functional address cannot be nil")
	}
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	c.funcAddr = addr
	if c.mode == AddressFunctional {
		c.stack.SetTxAddress(addr)
	}
	return nil
}

// SetAddressingMode switches between physical and functional addressing for requests.
func (c *UDSClient) SetAddressingMode(mode AddressingMode) error {
	c.reqMu.Lock()
	defer c.reqMu.Unlock()

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

// SendAndRecv sends a request and blocks until a response arrives, with timeout handling.
func (c *UDSClient) SendAndRecv(payload []byte, timeout time.Duration) ([]byte, error) {
	return c.RequestWithContext(context.Background(), payload, RequestOptions{
		Timeout:    timeout,
		MaxRetries: 0, // Keep backward compatibility: no retries
		RetryDelay: 0,
	})
}

// SendAndRecvWithAddressingMode sends one request with the specified addressing
// mode without changing the client's default addressing mode.
func (c *UDSClient) SendAndRecvWithAddressingMode(payload []byte, timeout time.Duration, mode AddressingMode) ([]byte, error) {
	return c.RequestWithContextAndAddressingMode(context.Background(), payload, RequestOptions{
		Timeout:    timeout,
		MaxRetries: 0, // Keep backward compatibility: no retries
		RetryDelay: 0,
	}, mode)
}

// RequestWithContext sends a UDS request and waits for a response, supporting Context cancellation.
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
		return nil, errors.New("request payload must not be empty")
	}

	c.reqMu.Lock()
	defer c.reqMu.Unlock()

	requestMode := c.mode
	if mode != nil {
		requestMode = *mode
	}
	if err := c.updateTxAddressLocked(requestMode); err != nil {
		return nil, err
	}

	requestSID := payload[0]
	expectedResponseSID := requestSID + 0x40                                                                      // Positive response SID = request SID + 0x40
	suppressPositive := hasSubFunctionSuppressPositive(requestSID) && len(payload) >= 2 && (payload[1]&0x80) != 0 // Recognize bit7 only for sub-function services

	var lastErr error
	var lastResp []byte
	for attempt := 0; attempt <= opts.MaxRetries; attempt++ {
		if attempt > 0 {
			log.Printf("UDS request retry (%d/%d), SID=0x%02X", attempt, opts.MaxRetries, requestSID)
			time.Sleep(opts.RetryDelay)
		}

		response, err := c.singleRequest(ctx, payload, opts.Timeout, suppressPositive)
		if err != nil {
			// Check for context cancellation
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return nil, err
			}

			// Check for UDS errors
			var udsErr *UDSError
			if errors.As(err, &udsErr) {
				// Retryable UDS error -> record last error/response, then retry
				if udsErr.IsRetryable() && attempt < opts.MaxRetries {
					lastErr = err
					lastResp = response
					continue
				}
				// Non-retryable UDS error -> return raw response and error
				return response, err
			}

			// Other errors
			return nil, err
		}

		// Validate response SID
		if len(response) > 0 && response[0] != expectedResponseSID {
			// Check for negative response
			if response[0] == 0x7F && len(response) >= 3 {
				return response, &UDSError{
					ServiceID: response[1],
					NRC:       response[2],
					Message:   getNRCDescription(response[2]),
				}
			}
			return response, fmt.Errorf("response SID mismatch: expected 0x%02X, got 0x%02X", expectedResponseSID, response[0])
		}

		return response, nil
	}

	if lastErr != nil {
		// Return the last response so the caller can inspect the raw frame
		return lastResp, fmt.Errorf("max retries reached (%d): %w", opts.MaxRetries, lastErr)
	}
	return nil, errors.New("unknown error")
}

// singleRequest performs a single request (without retry logic).
func (c *UDSClient) singleRequest(ctx context.Context, payload []byte, timeout time.Duration, suppressPositive bool) ([]byte, error) {
	if timeout <= 0 {
		return nil, errors.New("request timeout must be greater than 0")
	}

	c.drainStackRecv()
	c.drainTxErrors()

	// Clear any stale responses before sending
	c.stack.Send(payload) // Enqueue the packet for transmission

	deadline := time.NewTimer(timeout)
	defer deadline.Stop()

	// Use a local done channel to avoid nil-pointer when c.ctx is unset in tests
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
			return nil, errors.New("UDS client is closed")
		case err := <-txErrCh:
			return nil, err
		case <-deadline.C:
			if suppressPositive {
				return nil, nil // Suppress positive response: timeout means success
			}
			return nil, fmt.Errorf("waiting for response timed out (%v)", timeout)
		case data, ok := <-recvCh:
			if !ok {
				return nil, errors.New("transport receive channel closed")
			}
			// Check for negative response
			if len(data) >= 3 && data[0] == 0x7F {
				nrc := data[2]
				serviceSID := data[1]

				// Response Pending - reset timeout and keep waiting
				if nrc == RequestCorrectlyReceived_ResponsePending {
					if !deadline.Stop() {
						select {
						case <-deadline.C:
						default:
						}
					}
					deadline.Reset(responsePendingTimeout)
					continue
				}

				// Other negative responses
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

// Request is a simplified request helper using default options.
func (c *UDSClient) Request(payload []byte) ([]byte, error) {
	return c.RequestWithContext(context.Background(), payload, DefaultRequestOptions())
}

// RequestWithTimeout sends a request with a custom timeout.
func (c *UDSClient) RequestWithTimeout(payload []byte, timeout time.Duration) ([]byte, error) {
	opts := DefaultRequestOptions()
	opts.Timeout = timeout
	return c.RequestWithContext(context.Background(), payload, opts)
}

// SetFDMode allows dynamically switching CAN FD mode.
func (c *UDSClient) SetFDMode(isFD bool) {
	c.stack.SetFDMode(isFD)
}

// Close gracefully shuts down the client and releases all resources.
func (c *UDSClient) Close() {
	log.Println("Shutting down UDS client...")
	c.cancel()        // Signal all background goroutines to stop
	c.adapter.Close() // Close the adapter and hardware driver
}

// IsClosed reports whether the client has been closed.
func (c *UDSClient) IsClosed() bool {
	select {
	case <-c.ctx.Done():
		return true
	default:
		return false
	}
}
