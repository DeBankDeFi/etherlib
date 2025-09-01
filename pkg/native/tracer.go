package native

import (
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"strings"

	"sync/atomic"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/log"
)

type Tracer interface {
	vm.EVMLogger
	GetResult() (json.RawMessage, error)
	// Stop terminates execution of the tracer at the first opportune moment.
	Stop(err error)
}

// Context contains some contextual infos for a transaction execution that is not
// available from within the EVM object.
type Context struct {
	BlockHash   common.Hash // Hash of the block the tx is contained within (zero if dangling tx or call)
	BlockNumber *big.Int    // Number of the block the tx is contained within (zero if dangling tx or call)
	TxIndex     int         // Index of the transaction within a block (zero if dangling tx or call)
	TxHash      common.Hash // Hash of the transaction being traced (zero if dangling call)
}

// noopTracer is a go implementation of the Tracer interface which
// performs no action. It's mostly useful for testing purposes.
type noopTracer struct{}

// CaptureStart implements the EVMLogger interface to initialize the tracing operation.
func (t *noopTracer) CaptureStart(env *vm.EVM, from common.Address, to common.Address, create bool, input []byte, gas uint64, value *big.Int) {
}

// CaptureEnd is called after the call finishes to finalize the tracing.
func (t *noopTracer) CaptureEnd(output []byte, gasUsed uint64, err error) {
}

// CaptureState implements the EVMLogger interface to trace a single step of VM execution.
func (t *noopTracer) CaptureState(pc uint64, op vm.OpCode, gas, cost uint64, scope *vm.ScopeContext, rData []byte, depth int, err error) {
}

// CaptureFault implements the EVMLogger interface to trace an execution fault.
func (t *noopTracer) CaptureFault(pc uint64, op vm.OpCode, gas, cost uint64, _ *vm.ScopeContext, depth int, err error) {
}

// CaptureEnter is called when EVM enters a new scope (via call, create or selfdestruct).
func (t *noopTracer) CaptureEnter(typ vm.OpCode, from common.Address, to common.Address, input []byte, gas uint64, value *big.Int) {
}

// CaptureExit is called when EVM exits a scope, even if the scope didn't
// execute any code.
func (t *noopTracer) CaptureExit(output []byte, gasUsed uint64, err error) {
}

func (*noopTracer) CaptureTxStart(gasLimit uint64) {}

func (*noopTracer) CaptureTxEnd(restGas uint64) {}

// GetResult returns an empty json object.
func (t *noopTracer) GetResult() (json.RawMessage, error) {
	return json.RawMessage(`{}`), nil
}

// Stop terminates execution of the tracer at the first opportune moment.
func (t *noopTracer) Stop(err error) {
}

type callLog struct {
	Address common.Address `json:"address"`
	Topics  []common.Hash  `json:"topics"`
	Data    hexutil.Bytes  `json:"data"`
	// Position of the log relative to subcalls within the same trace
	// See https://github.com/ethereum/go-ethereum/pull/28389 for details
	Position hexutil.Uint `json:"position"`
}

type callFrame struct {
	Type         vm.OpCode       `json:"-"`
	From         common.Address  `json:"from"`
	Gas          uint64          `json:"gas"`
	GasUsed      uint64          `json:"gasUsed"`
	To           *common.Address `json:"to,omitempty" rlp:"optional"`
	Input        []byte          `json:"input" rlp:"optional"`
	Output       []byte          `json:"output,omitempty" rlp:"optional"`
	Error        string          `json:"error,omitempty" rlp:"optional"`
	RevertReason string          `json:"revertReason,omitempty"`
	Calls        []callFrame     `json:"calls,omitempty" rlp:"optional"`
	Logs         []callLog       `json:"logs,omitempty" rlp:"optional"`
	// Placed at end on purpose. The RLP will be decoded to 0 instead of
	// nil if there are non-empty elements after in the struct.
	Value *big.Int `json:"value,omitempty" rlp:"optional"`
}

func (f callFrame) TypeString() string {
	return f.Type.String()
}

func (f callFrame) failed() bool {
	return len(f.Error) > 0
}

func (f *callFrame) processOutput(output []byte, err error) {
	output = common.CopyBytes(output)
	if err == nil {
		f.Output = output
		return
	}
	f.Error = err.Error()
	if f.Type == vm.CREATE || f.Type == vm.CREATE2 {
		f.To = nil
	}
	if !errors.Is(err, vm.ErrExecutionReverted) || len(output) == 0 {
		return
	}
	f.Output = output
	if len(output) < 4 {
		return
	}
	if unpacked, err := abi.UnpackRevert(output); err == nil {
		f.RevertReason = unpacked
	}
}

type callTracer struct {
	noopTracer
	callstack []callFrame
	config    callTracerConfig
	gasLimit  uint64
	interrupt atomic.Bool // Atomic flag to signal execution interruption
	reason    error       // Textual reason for the interruption
}

type callTracerConfig struct {
	OnlyTopCall bool `json:"onlyTopCall"` // If true, call tracer won't collect any subcalls
	WithLog     bool `json:"withLog"`     // If true, call tracer will collect event logs
}

// newCallTracer returns a native go tracer which tracks
// call frames of a tx, and implements vm.EVMLogger.
func newCallTracer(ctx *Context, cfg json.RawMessage) (Tracer, error) {
	var config callTracerConfig
	if cfg != nil {
		if err := json.Unmarshal(cfg, &config); err != nil {
			return nil, err
		}
	}
	// First callframe contains tx context info
	// and is populated on start and end.
	return &callTracer{callstack: make([]callFrame, 1), config: config}, nil
}

// CaptureStart implements the EVMLogger interface to initialize the tracing operation.
func (t *callTracer) CaptureStart(env *vm.EVM, from common.Address, to common.Address, create bool, input []byte, gas uint64, value *big.Int) {
	toCopy := to
	t.callstack[0] = callFrame{
		Type:  vm.CALL,
		From:  from,
		To:    &toCopy,
		Input: common.CopyBytes(input),
		Gas:   t.gasLimit,
		Value: value,
	}
	if create {
		t.callstack[0].Type = vm.CREATE
	}
}

// CaptureEnd is called after the call finishes to finalize the tracing.
func (t *callTracer) CaptureEnd(output []byte, gasUsed uint64, err error) {
	t.callstack[0].processOutput(output, err)
}

// CaptureState implements the EVMLogger interface to trace a single step of VM execution.
func (t *callTracer) CaptureState(pc uint64, op vm.OpCode, gas, cost uint64, scope *vm.ScopeContext, rData []byte, depth int, err error) {
	// skip if the previous op caused an error
	if err != nil {
		return
	}
	// Only logs need to be captured via opcode processing
	if !t.config.WithLog {
		return
	}
	// Avoid processing nested calls when only caring about top call
	if t.config.OnlyTopCall && depth > 0 {
		return
	}
	// Skip if tracing was interrupted
	if t.interrupt.Load() {
		return
	}
	switch op {
	case vm.LOG0, vm.LOG1, vm.LOG2, vm.LOG3, vm.LOG4:
		size := int(op - vm.LOG0)

		stack := scope.Stack
		stackData := stack.Data()

		// Don't modify the stack
		mStart := stackData[len(stackData)-1]
		mSize := stackData[len(stackData)-2]
		topics := make([]common.Hash, size)
		for i := 0; i < size; i++ {
			topic := stackData[len(stackData)-2-(i+1)]
			topics[i] = common.Hash(topic.Bytes32())
		}

		data, err := GetMemoryCopyPadded(scope.Memory, int64(mStart.Uint64()), int64(mSize.Uint64()))
		if err != nil {
			// mSize was unrealistically large
			log.Warn("failed to copy CREATE2 input", "err", err, "tracer", "callTracer", "offset", mStart, "size", mSize)
			return
		}

		log := callLog{
			Address:  scope.Contract.Address(),
			Topics:   topics,
			Data:     hexutil.Bytes(data),
			Position: hexutil.Uint(len(t.callstack[len(t.callstack)-1].Calls)),
		}
		t.callstack[len(t.callstack)-1].Logs = append(t.callstack[len(t.callstack)-1].Logs, log)
	}
}

// CaptureEnter is called when EVM enters a new scope (via call, create or selfdestruct).
func (t *callTracer) CaptureEnter(typ vm.OpCode, from common.Address, to common.Address, input []byte, gas uint64, value *big.Int) {
	if t.config.OnlyTopCall {
		return
	}
	// Skip if tracing was interrupted
	if t.interrupt.Load() {
		return
	}

	toCopy := to
	call := callFrame{
		Type:  typ,
		From:  from,
		To:    &toCopy,
		Input: common.CopyBytes(input),
		Gas:   gas,
		Value: value,
	}
	t.callstack = append(t.callstack, call)
}

// CaptureExit is called when EVM exits a scope, even if the scope didn't
// execute any code.
func (t *callTracer) CaptureExit(output []byte, gasUsed uint64, err error) {
	if t.config.OnlyTopCall {
		return
	}
	size := len(t.callstack)
	if size <= 1 {
		return
	}
	// pop call
	call := t.callstack[size-1]
	t.callstack = t.callstack[:size-1]
	size -= 1

	call.GasUsed = gasUsed
	call.processOutput(output, err)
	t.callstack[size-1].Calls = append(t.callstack[size-1].Calls, call)
}

func (t *callTracer) CaptureTxStart(gasLimit uint64) {
	t.gasLimit = gasLimit
}

func (t *callTracer) CaptureTxEnd(restGas uint64) {
	t.callstack[0].GasUsed = t.gasLimit - restGas
	if t.config.WithLog {
		// Logs are not emitted when the call fails
		clearFailedLogs(&t.callstack[0], false)
	}
}

// GetResult returns the json-encoded nested list of call traces, and any
// error arising from the encoding or forceful termination (via `Stop`).
func (t *callTracer) GetResult() (json.RawMessage, error) {
	if len(t.callstack) != 1 {
		return nil, errors.New("incorrect number of top-level calls")
	}

	res, err := json.Marshal(t.callstack[0])
	if err != nil {
		return nil, err
	}
	return json.RawMessage(res), t.reason
}

// Stop terminates execution of the tracer at the first opportune moment.
func (t *callTracer) Stop(err error) {
	t.reason = err
	t.interrupt.Store(true)
}

// clearFailedLogs clears the logs of a callframe and all its children
// in case of execution failure.
func clearFailedLogs(cf *callFrame, parentFailed bool) {
	failed := cf.failed() || parentFailed
	// Clear own logs
	if failed {
		cf.Logs = nil
	}
	for i := range cf.Calls {
		clearFailedLogs(&cf.Calls[i], failed)
	}
}

var parityErrorMapping = map[string]string{
	"contract creation code storage out of gas": "Out of gas",
	"out of gas":                      "Out of gas",
	"gas uint64 overflow":             "Out of gas",
	"max code size exceeded":          "Out of gas",
	"invalid jump destination":        "Bad jump destination",
	"execution reverted":              "Reverted",
	"return data out of bounds":       "Out of bounds",
	"stack limit reached 1024 (1023)": "Out of stack",
	"precompiled failed":              "Built-in failed",
	"invalid input length":            "Built-in failed",
}

var parityErrorMappingStartingWith = map[string]string{
	"invalid opcode:": "Bad instruction",
	"stack underflow": "Stack underflow",
}

// flatCallFrame is a standalone callframe.
type flatCallFrame struct {
	Action              flatCallAction  `json:"action"`
	BlockHash           *common.Hash    `json:"blockHash"`
	BlockNumber         uint64          `json:"blockNumber"`
	Error               string          `json:"error,omitempty"`
	Result              *flatCallResult `json:"result,omitempty"`
	Subtraces           int             `json:"subtraces"`
	TraceAddress        []int           `json:"traceAddress"`
	TransactionHash     *common.Hash    `json:"transactionHash"`
	TransactionPosition uint64          `json:"transactionPosition"`
	Type                string          `json:"type"`
}

type flatCallAction struct {
	Author         *common.Address `json:"author,omitempty"`
	RewardType     string          `json:"rewardType,omitempty"`
	SelfDestructed *common.Address `json:"address,omitempty"`
	Balance        *big.Int        `json:"balance,omitempty"`
	CallType       string          `json:"callType,omitempty"`
	CreationMethod string          `json:"creationMethod,omitempty"`
	From           *common.Address `json:"from,omitempty"`
	Gas            *uint64         `json:"gas,omitempty"`
	Init           *[]byte         `json:"init,omitempty"`
	Input          *[]byte         `json:"input,omitempty"`
	RefundAddress  *common.Address `json:"refundAddress,omitempty"`
	To             *common.Address `json:"to,omitempty"`
	Value          *big.Int        `json:"value,omitempty"`
}

type flatCallActionMarshaling struct {
	Balance *hexutil.Big
	Gas     *hexutil.Uint64
	Init    *hexutil.Bytes
	Input   *hexutil.Bytes
	Value   *hexutil.Big
}

type flatCallResult struct {
	Address *common.Address `json:"address,omitempty"`
	Code    *[]byte         `json:"code,omitempty"`
	GasUsed *uint64         `json:"gasUsed,omitempty"`
	Output  *[]byte         `json:"output,omitempty"`
}

type flatCallResultMarshaling struct {
	Code    *hexutil.Bytes
	GasUsed *hexutil.Uint64
	Output  *hexutil.Bytes
}

// flatCallTracer reports call frame information of a tx in a flat format, i.e.
// as opposed to the nested format of `callTracer`.
type flatCallTracer struct {
	tracer            *callTracer
	config            flatCallTracerConfig
	ctx               *Context         // Holds tracer context data
	reason            error            // Textual reason for the interruption
	activePrecompiles []common.Address // Updated on CaptureStart based on given rules
}

type flatCallTracerConfig struct {
	ConvertParityErrors bool `json:"convertParityErrors"` // If true, call tracer converts errors to parity format
	IncludePrecompiles  bool `json:"includePrecompiles"`  // If true, call tracer includes calls to precompiled contracts
}

// newFlatCallTracer returns a new flatCallTracer.
func newFlatCallTracer(ctx *Context, cfg json.RawMessage) (Tracer, error) {
	var config flatCallTracerConfig
	if cfg != nil {
		if err := json.Unmarshal(cfg, &config); err != nil {
			return nil, err
		}
	}

	// Create inner call tracer with default configuration, don't forward
	// the OnlyTopCall or WithLog to inner for now
	tracer, err := newCallTracer(ctx, nil)
	if err != nil {
		return nil, err
	}
	t, ok := tracer.(*callTracer)
	if !ok {
		return nil, errors.New("internal error: embedded tracer has wrong type")
	}

	return &flatCallTracer{tracer: t, ctx: ctx, config: config}, nil
}

func NewFlatCallTracer(ctx *Context) (Tracer, error) {
	return newFlatCallTracer(ctx, json.RawMessage("{}"))
}

// CaptureStart implements the EVMLogger interface to initialize the tracing operation.
func (t *flatCallTracer) CaptureStart(env *vm.EVM, from common.Address, to common.Address, create bool, input []byte, gas uint64, value *big.Int) {
	t.tracer.CaptureStart(env, from, to, create, input, gas, value)
	// Update list of precompiles based on current block
	rules := env.ChainConfig().Rules(env.Context.BlockNumber, env.Context.Random != nil, env.Context.Time)
	t.activePrecompiles = vm.ActivePrecompiles(rules)
}

// CaptureEnd is called after the call finishes to finalize the tracing.
func (t *flatCallTracer) CaptureEnd(output []byte, gasUsed uint64, err error) {
	t.tracer.CaptureEnd(output, gasUsed, err)
}

// CaptureState implements the EVMLogger interface to trace a single step of VM execution.
func (t *flatCallTracer) CaptureState(pc uint64, op vm.OpCode, gas, cost uint64, scope *vm.ScopeContext, rData []byte, depth int, err error) {
	t.tracer.CaptureState(pc, op, gas, cost, scope, rData, depth, err)
}

// CaptureFault implements the EVMLogger interface to trace an execution fault.
func (t *flatCallTracer) CaptureFault(pc uint64, op vm.OpCode, gas, cost uint64, scope *vm.ScopeContext, depth int, err error) {
	t.tracer.CaptureFault(pc, op, gas, cost, scope, depth, err)
}

// CaptureEnter is called when EVM enters a new scope (via call, create or selfdestruct).
func (t *flatCallTracer) CaptureEnter(typ vm.OpCode, from common.Address, to common.Address, input []byte, gas uint64, value *big.Int) {
	t.tracer.CaptureEnter(typ, from, to, input, gas, value)

	// Child calls must have a value, even if it's zero.
	// Practically speaking, only STATICCALL has nil value. Set it to zero.
	if t.tracer.callstack[len(t.tracer.callstack)-1].Value == nil && value == nil {
		t.tracer.callstack[len(t.tracer.callstack)-1].Value = big.NewInt(0)
	}
}

// CaptureExit is called when EVM exits a scope, even if the scope didn't
// execute any code.
func (t *flatCallTracer) CaptureExit(output []byte, gasUsed uint64, err error) {
	t.tracer.CaptureExit(output, gasUsed, err)

	// Parity traces don't include CALL/STATICCALLs to precompiles.
	// By default we remove them from the callstack.
	if t.config.IncludePrecompiles {
		return
	}
	var (
		// call has been nested in parent
		parent = t.tracer.callstack[len(t.tracer.callstack)-1]
		call   = parent.Calls[len(parent.Calls)-1]
		typ    = call.Type
		to     = call.To
	)
	if typ == vm.CALL || typ == vm.STATICCALL {
		if t.isPrecompiled(*to) {
			t.tracer.callstack[len(t.tracer.callstack)-1].Calls = parent.Calls[:len(parent.Calls)-1]
		}
	}
}

func (t *flatCallTracer) CaptureTxStart(gasLimit uint64) {
	t.tracer.CaptureTxStart(gasLimit)
}

func (t *flatCallTracer) CaptureTxEnd(restGas uint64) {
	t.tracer.CaptureTxEnd(restGas)
}

// GetResult returns an empty json object.
func (t *flatCallTracer) GetResult() (json.RawMessage, error) {
	if len(t.tracer.callstack) < 1 {
		return nil, errors.New("invalid number of calls")
	}

	flat, err := flatFromNested(&t.tracer.callstack[0], []int{}, t.config.ConvertParityErrors, t.ctx)
	if err != nil {
		return nil, err
	}

	res, err := json.Marshal(flat)
	if err != nil {
		return nil, err
	}
	return res, t.reason
}

// Stop terminates execution of the tracer at the first opportune moment.
func (t *flatCallTracer) Stop(err error) {
	t.tracer.Stop(err)
}

// isPrecompiled returns whether the addr is a precompile.
func (t *flatCallTracer) isPrecompiled(addr common.Address) bool {
	for _, p := range t.activePrecompiles {
		if p == addr {
			return true
		}
	}
	return false
}

func flatFromNested(input *callFrame, traceAddress []int, convertErrs bool, ctx *Context) (output []flatCallFrame, err error) {
	var frame *flatCallFrame
	switch input.Type {
	case vm.CREATE, vm.CREATE2:
		frame = newFlatCreate(input)
	case vm.SELFDESTRUCT:
		frame = newFlatSelfdestruct(input)
	case vm.CALL, vm.STATICCALL, vm.CALLCODE, vm.DELEGATECALL:
		frame = newFlatCall(input)
	default:
		return nil, fmt.Errorf("unrecognized call frame type: %s", input.Type)
	}

	frame.TraceAddress = traceAddress
	frame.Error = input.Error
	frame.Subtraces = len(input.Calls)
	fillCallFrameFromContext(frame, ctx)
	if convertErrs {
		convertErrorToParity(frame)
	}

	// Revert output contains useful information (revert reason).
	// Otherwise discard result.
	if input.Error != "" && input.Error != vm.ErrExecutionReverted.Error() {
		frame.Result = nil
	}

	output = append(output, *frame)
	if len(input.Calls) > 0 {
		for i, childCall := range input.Calls {
			childAddr := childTraceAddress(traceAddress, i)
			childCallCopy := childCall
			flat, err := flatFromNested(&childCallCopy, childAddr, convertErrs, ctx)
			if err != nil {
				return nil, err
			}
			output = append(output, flat...)
		}
	}

	return output, nil
}

func newFlatCreate(input *callFrame) *flatCallFrame {
	var (
		actionInit = input.Input[:]
		resultCode = input.Output[:]
	)

	return &flatCallFrame{
		Type: strings.ToLower(vm.CREATE.String()),
		Action: flatCallAction{
			From:  &input.From,
			Gas:   &input.Gas,
			Value: input.Value,
			Init:  &actionInit,
		},
		Result: &flatCallResult{
			GasUsed: &input.GasUsed,
			Address: input.To,
			Code:    &resultCode,
		},
	}
}

func newFlatCall(input *callFrame) *flatCallFrame {
	var (
		actionInput  = input.Input[:]
		resultOutput = input.Output[:]
	)

	return &flatCallFrame{
		Type: strings.ToLower(vm.CALL.String()),
		Action: flatCallAction{
			From:     &input.From,
			To:       input.To,
			Gas:      &input.Gas,
			Value:    input.Value,
			CallType: strings.ToLower(input.Type.String()),
			Input:    &actionInput,
		},
		Result: &flatCallResult{
			GasUsed: &input.GasUsed,
			Output:  &resultOutput,
		},
	}
}

func newFlatSelfdestruct(input *callFrame) *flatCallFrame {
	return &flatCallFrame{
		Type: "suicide",
		Action: flatCallAction{
			SelfDestructed: &input.From,
			Balance:        input.Value,
			RefundAddress:  input.To,
		},
	}
}

func fillCallFrameFromContext(callFrame *flatCallFrame, ctx *Context) {
	if ctx == nil {
		return
	}
	if ctx.BlockHash != (common.Hash{}) {
		callFrame.BlockHash = &ctx.BlockHash
	}
	if ctx.BlockNumber != nil {
		callFrame.BlockNumber = ctx.BlockNumber.Uint64()
	}
	if ctx.TxHash != (common.Hash{}) {
		callFrame.TransactionHash = &ctx.TxHash
	}
	callFrame.TransactionPosition = uint64(ctx.TxIndex)
}

func convertErrorToParity(call *flatCallFrame) {
	if call.Error == "" {
		return
	}

	if parityError, ok := parityErrorMapping[call.Error]; ok {
		call.Error = parityError
	} else {
		for gethError, parityError := range parityErrorMappingStartingWith {
			if strings.HasPrefix(call.Error, gethError) {
				call.Error = parityError
			}
		}
	}
}

func childTraceAddress(a []int, i int) []int {
	child := make([]int, 0, len(a)+1)
	child = append(child, a...)
	child = append(child, i)
	return child
}

const (
	memoryPadLimit = 1024 * 1024
)

// GetMemoryCopyPadded returns offset + size as a new slice.
// It zero-pads the slice if it extends beyond memory bounds.
func GetMemoryCopyPadded(m *vm.Memory, offset, size int64) ([]byte, error) {
	if offset < 0 || size < 0 {
		return nil, errors.New("offset or size must not be negative")
	}
	if int(offset+size) < m.Len() { // slice fully inside memory
		return m.GetCopy(offset, size), nil
	}
	paddingNeeded := int(offset+size) - m.Len()
	if paddingNeeded > memoryPadLimit {
		return nil, fmt.Errorf("reached limit for padding memory slice: %d", paddingNeeded)
	}
	cpy := make([]byte, size)
	if overlap := int64(m.Len()) - offset; overlap > 0 {
		copy(cpy, m.GetPtr(offset, overlap))
	}
	return cpy, nil
}
