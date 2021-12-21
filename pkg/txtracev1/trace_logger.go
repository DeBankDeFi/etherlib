// Copyright 2021 The go-ethereum Authors
// This file is part of the go-ethereum library.
//
// The go-ethereum library is free software: you can redistribute it and/or modify
// it under the terms of the GNU Lesser General Public License as published by
// the Free Software Foundation, either version 3 of the License, or
// (at your option) any later version.
//
// The go-ethereum library is distributed in the hope that it will be useful,
// but WITHOUT ANY WARRANTY; without even the implied warranty of
// MERCHANTABILITY or FITNESS FOR A PARTICULAR PURPOSE. See the
// GNU Lesser General Public License for more details.
//
// You should have received a copy of the GNU Lesser General Public License
// along with the go-ethereum library. If not, see <http://www.gnu.org/licenses/>.

package txtrace

import (
	"context"
	"math/big"
	"strings"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
	"github.com/holiman/uint256"
)

var _ vm.EVMLogger = (*OeTracer)(nil)

// OeTracer OpenEthereum-style tracer
type OeTracer struct {
	store       Store
	from        *common.Address
	to          *common.Address
	newAddress  *common.Address
	blockHash   common.Hash
	tx          common.Hash
	txIndex     uint
	blockNumber big.Int
	value       big.Int

	gasUsed      uint64
	traceHolder  *CallTrace
	inputData    []byte
	state        []depthState
	traceAddress []uint32
	stack        []*big.Int
	reverted     bool
	output       []byte
	err          error
}

// NewOeTracer creates new instance of trace creator with underlying database.
func NewOeTracer(db Store) *OeTracer {
	ot := OeTracer{
		store: db,
		stack: make([]*big.Int, 30),
	}
	return &ot
}

// stackPeek returns object from stack at given position from end of stack
func stackPeek(stackData []uint256.Int, pos int) *big.Int {
	if len(stackData) <= pos || pos < 0 {
		log.Warn("Tracer accessed out of bound stack", "size", len(stackData), "index", pos)
		return new(big.Int)
	}
	return new(big.Int).Set(stackData[len(stackData)-1-pos].ToBig())
}

func memorySlice(memory []byte, offset, size int64) []byte {
	if size == 0 {
		return []byte{}
	}
	if offset+size < offset || offset < 0 {
		log.Warn("Tracer accessed out of bound memory", "offset", offset, "size", size)
		return nil
	}
	if len(memory) < int(offset+size) {
		log.Warn("Tracer accessed out of bound memory", "available", len(memory), "offset", offset, "size", size)
		return nil
	}
	return memory[offset : offset+size]
}

// CaptureStart implements the tracer interface to initialize the tracing operation.
func (ot *OeTracer) CaptureStart(env *vm.EVM, from common.Address, to common.Address, create bool, input []byte, gas uint64, value *big.Int) {
	// Create main trace holder
	tracesHolder := CallTrace{
		Actions: make([]ActionTrace, 0),
	}

	// Nil `to` address means it's a CREATE* CALL
	callType := CREATE
	var newAddress *common.Address
	if ot.to != nil {
		callType = CALL
	} else { // callType == CREATE
		newAddress = &to
	}

	// Store input data
	ot.inputData = input
	if gas == 0 && ot.gasUsed != 0 {
		gas = ot.gasUsed
	}

	// Make transaction trace root object
	rootTrace := NewActionTrace(ot.blockHash, ot.blockNumber, ot.tx, uint64(ot.txIndex), callType)
	var txAction *TAction
	if CREATE == callType {
		txAction = NewTAction(ot.from, ot.to, gas, ot.inputData, hexutil.Big(ot.value), nil)
		if newAddress != nil {
			rootTrace.Result.Address = newAddress
			rootTrace.Result.Code = ot.output
		}
	} else {
		txAction = NewTAction(ot.from, ot.to, gas, ot.inputData, hexutil.Big(ot.value), &callType)
		out := hexutil.Bytes(ot.output)
		rootTrace.Result.Output = &out
	}
	rootTrace.Action = *txAction

	// Add root object into Tracer
	tracesHolder.AddTrace(rootTrace)
	ot.traceHolder = &tracesHolder

	// Init all needed variables
	ot.state = []depthState{{0, create}}
	ot.traceAddress = make([]uint32, 0)
	ot.traceHolder.Stack = append(ot.traceHolder.Stack, &ot.traceHolder.Actions[len(ot.traceHolder.Actions)-1])
}

// CaptureState implements creating of traces based on getting opCodes from evm during contract processing
func (ot *OeTracer) CaptureState(pc uint64, op vm.OpCode, gas, cost uint64, scope *vm.ScopeContext, rData []byte, depth int, err error) {
	stack, memory, contract := scope.Stack, scope.Memory, scope.Contract
	// When going back from inner call
	if lastState(ot.state).level == depth {
		result := ot.traceHolder.Stack[len(ot.traceHolder.Stack)-1].Result
		if lastState(ot.state).create && result != nil {
			if len(stack.Data()) > 0 {
				addr := common.BytesToAddress(stackPeek(stack.Data(), 0).Bytes())
				result.Address = &addr
				result.GasUsed = hexutil.Uint64(gas)
			}
		}
		ot.traceAddress = removeTraceAddressLevel(ot.traceAddress, depth)
		ot.state = ot.state[:len(ot.state)-1]
		ot.traceHolder.Stack = ot.traceHolder.Stack[:len(ot.traceHolder.Stack)-1]
	}

	// We only care about system opcodes, faster if we pre-check once.
	if !(op&0xf0 == 0xf0) && op != 0x0 {
		return
	}

	// Match processed instruction and create trace based on it
	switch op {
	case vm.CREATE, vm.CREATE2:
		ot.traceAddress = addTraceAddress(ot.traceAddress, depth)
		fromTrace := ot.traceHolder.Stack[len(ot.traceHolder.Stack)-1]

		// Get input data from memory
		offset := stackPeek(stack.Data(), 1).Int64()
		inputSize := stackPeek(stack.Data(), 2).Int64()
		var input []byte
		if inputSize > 0 {
			input = make([]byte, inputSize)
			copy(input, memorySlice(memory.Data(), offset, inputSize))
		}

		// Create new trace
		trace := NewActionTraceFromTrace(fromTrace, CREATE, ot.traceAddress)
		from := contract.Address()
		traceAction := NewTAction(&from, nil, gas, input, fromTrace.Action.Value, nil)
		trace.Action = *traceAction
		trace.Result.GasUsed = hexutil.Uint64(gas)
		fromTrace.childTraces = append(fromTrace.childTraces, trace)
		ot.traceHolder.Stack = append(ot.traceHolder.Stack, trace)
		ot.state = append(ot.state, depthState{depth, true})

	case vm.CALL, vm.CALLCODE, vm.DELEGATECALL, vm.STATICCALL:
		var (
			inOffset, inSize   int64
			retOffset, retSize uint64
			input              []byte
			value              = big.NewInt(0)
		)

		if vm.DELEGATECALL == op || vm.STATICCALL == op {
			inOffset = stackPeek(stack.Data(), 2).Int64()
			inSize = stackPeek(stack.Data(), 3).Int64()
			retOffset = stackPeek(stack.Data(), 4).Uint64()
			retSize = stackPeek(stack.Data(), 5).Uint64()
		} else {
			inOffset = stackPeek(stack.Data(), 3).Int64()
			inSize = stackPeek(stack.Data(), 4).Int64()
			retOffset = stackPeek(stack.Data(), 5).Uint64()
			retSize = stackPeek(stack.Data(), 6).Uint64()
			// only CALL and CALLCODE need `value` field
			value = stackPeek(stack.Data(), 2)
		}
		if inSize > 0 {
			input = make([]byte, inSize)
			copy(input, memorySlice(memory.Data(), inOffset, inSize))
		}
		ot.traceAddress = addTraceAddress(ot.traceAddress, depth)
		fromTrace := ot.traceHolder.Stack[len(ot.traceHolder.Stack)-1]
		// create new trace
		trace := NewActionTraceFromTrace(fromTrace, CALL, ot.traceAddress)
		from := contract.Address()
		addr := common.BytesToAddress(stackPeek(stack.Data(), 1).Bytes())
		callType := strings.ToLower(op.String())
		traceAction := NewTAction(&from, &addr, gas, input, hexutil.Big(*value), &callType)
		trace.Action = *traceAction
		fromTrace.childTraces = append(fromTrace.childTraces, trace)
		trace.Result.RetOffset = retOffset
		trace.Result.RetSize = retSize
		ot.traceHolder.Stack = append(ot.traceHolder.Stack, trace)
		ot.state = append(ot.state, depthState{depth, false})

	case vm.RETURN, vm.STOP:
		if ot.reverted {
			ot.traceHolder.Stack[len(ot.traceHolder.Stack)-1].Result = nil
			ot.traceHolder.Stack[len(ot.traceHolder.Stack)-1].Error = "Reverted"
		} else {
			result := ot.traceHolder.Stack[len(ot.traceHolder.Stack)-1].Result
			var data []byte

			if vm.STOP != op {
				offset := stackPeek(stack.Data(), 0).Int64()
				size := stackPeek(stack.Data(), 1).Int64()
				if size > 0 {
					data = make([]byte, size)
					copy(data, memorySlice(memory.Data(), offset, size))
				}
			}

			if lastState(ot.state).create {
				result.Code = data
			} else {
				result.GasUsed = hexutil.Uint64(gas)
				out := hexutil.Bytes(data)
				result.Output = &out
			}
		}

	case vm.REVERT:
		ot.reverted = true
		ot.traceHolder.Stack[len(ot.traceHolder.Stack)-1].Result = nil
		ot.traceHolder.Stack[len(ot.traceHolder.Stack)-1].Error = "Reverted"

	case vm.SELFDESTRUCT:
		ot.traceAddress = addTraceAddress(ot.traceAddress, depth)
		fromTrace := ot.traceHolder.Stack[len(ot.traceHolder.Stack)-1]
		trace := NewActionTraceFromTrace(fromTrace, SELFDESTRUCT, ot.traceAddress)
		action := fromTrace.Action

		from := contract.Address()
		traceAction := NewTAction(nil, nil, 0, nil, action.Value, nil)
		traceAction.Address = &from
		// set refund values
		refundAddress := common.BytesToAddress(stackPeek(stack.Data(), 0).Bytes())
		traceAction.RefundAddress = &refundAddress
		// Add `balance` field for convenient usage, set to 0x0
		traceAction.Balance = (*hexutil.Big)(big.NewInt(0))
		trace.Action = *traceAction
		fromTrace.childTraces = append(fromTrace.childTraces, trace)
	}
}

func (ot *OeTracer) CaptureEnter(typ vm.OpCode, from common.Address, to common.Address, input []byte, gas uint64, value *big.Int) {
}

func (ot *OeTracer) CaptureExit(output []byte, gasUsed uint64, err error) {}

// CaptureEnd is called after the call complete and finalize the tracing.
func (ot *OeTracer) CaptureEnd(output []byte, gasUsed uint64, t time.Duration, err error) {
	log.Debug("OeTracer CaptureEND", "txHash", ot.tx.String(), "duration", common.PrettyDuration(t), "gasUsed", gasUsed)
	if gasUsed > 0 {
		if ot.traceHolder.Actions[0].Result != nil {
			ot.traceHolder.Actions[0].Result.GasUsed = hexutil.Uint64(gasUsed)
		}
		ot.traceHolder.lastTrace().Action.Gas = hexutil.Uint64(gasUsed)

		ot.gasUsed = gasUsed
	}
	ot.output = output
}

// CaptureFault implements the Tracer interface to trace an execution fault
// while running an opcode.
func (ot *OeTracer) CaptureFault(pc uint64, op vm.OpCode, gas, cost uint64, scope *vm.ScopeContext, depth int, err error) {
}

// Reset function to be able to reuse logger
func (ot *OeTracer) reset() {
	ot.to = nil
	ot.from = nil
	ot.inputData = nil
	ot.traceHolder = nil
	ot.reverted = false
}

// SetMessage basic setter that fill block and tx info into tracer.
func (ot *OeTracer) SetMessage(blockNr *big.Int, blockHash common.Hash, tx common.Hash, txIndex uint, from common.Address, to *common.Address, value big.Int) {
	ot.blockNumber = *blockNr
	ot.blockHash = blockHash
	ot.tx = tx
	ot.txIndex = txIndex
	ot.from = &from
	ot.to = to
	ot.value = value
}

// SetTx basic setter
func (ot *OeTracer) SetTx(tx common.Hash) {
	ot.tx = tx
}

// SetFrom basic setter
func (ot *OeTracer) SetFrom(from common.Address) {
	ot.from = &from
}

// SetTo basic setter
func (ot *OeTracer) SetTo(to *common.Address) {
	ot.to = to
}

// SetValue basic setter
func (ot *OeTracer) SetValue(value big.Int) {
	ot.value = value
}

// SetBlockHash basic setter
func (ot *OeTracer) SetBlockHash(blockHash common.Hash) {
	ot.blockHash = blockHash
}

// SetBlockNumber basic setter
func (ot *OeTracer) SetBlockNumber(blockNumber *big.Int) {
	ot.blockNumber = *blockNumber
}

// SetTxIndex basic setter
func (ot *OeTracer) SetTxIndex(txIndex uint) {
	ot.txIndex = txIndex
}

// SetNewAddress basic setter
func (ot *OeTracer) SetNewAddress(newAddress common.Address) {
	ot.newAddress = &newAddress
}

// SetGasUsed basic setter
func (ot *OeTracer) SetGasUsed(gasUsed uint64) {
	ot.gasUsed = gasUsed
}

// Finalize finalizes trace process and stores result into key-value persistent store
func (ot *OeTracer) Finalize() {
	if ot.traceHolder != nil {
		ot.traceHolder.lastTrace().Action.Gas = hexutil.Uint64(ot.gasUsed)
		if ot.traceHolder.lastTrace().Result != nil {
			ot.traceHolder.lastTrace().Result.GasUsed = hexutil.Uint64(ot.gasUsed)
		}
		ot.traceHolder.processLastTrace()
	}
}

// PersistTrace save traced tx result to underlying k-v store.
func (ot *OeTracer) PersistTrace() {
	if ot.traceHolder == nil {
		ot.traceHolder = &CallTrace{}
		ot.traceHolder.AddTrace(GetErrorTrace(ot.blockHash, ot.blockNumber, ot.to, ot.tx, ot.gasUsed, ot.err))

	}

	if ot.store != nil {
		// Convert trace objects to json byte array and save it
		var actions ActionTraces = ot.traceHolder.Actions
		if len(actions) == 0 {
			log.Warn("Empty tx trace found", "txHash", ot.tx.String())
			return
		}
		tracesBytes, err := rlp.EncodeToBytes(&actions)
		if err != nil {
			log.Error("Failed to encode tx trace", "txHash", ot.tx.String(), "err", err.Error())
			return
		}
		if err := ot.store.WriteTxTrace(context.Background(), ot.tx, tracesBytes); err != nil {
			log.Error("Failed to persist tx trace to database", "txHash", ot.tx.String(), "err", err.Error())
			return
		}
		log.Debug("Persist tx trace to database", "txHash", ot.tx.String(), "bytes", len(tracesBytes))
	}
	ot.reset()
}

// GetResult returns action traces after recording evm process
func (ot *OeTracer) GetResult() *[]ActionTrace {
	if ot.traceHolder != nil {
		return &ot.traceHolder.Actions
	}
	empty := make([]ActionTrace, 0)
	return &empty
}

// CallTrace is struct for holding tracing results.
type CallTrace struct {
	Actions []ActionTrace  `json:"result"`
	Stack   []*ActionTrace `json:"-"`
}

// AddTrace Append trace to call trace list
func (callTrace *CallTrace) AddTrace(actionTrace *ActionTrace) {
	if callTrace.Actions == nil {
		callTrace.Actions = make([]ActionTrace, 0)
	}
	callTrace.Actions = append(callTrace.Actions, *actionTrace)
}

// AddTraces Append traces to call trace list
func (callTrace *CallTrace) AddTraces(traces *[]ActionTrace) {
	for _, trace := range *traces {
		callTrace.AddTrace(&trace)
	}
}

// lastTrace Get last trace in call trace list
func (callTrace *CallTrace) lastTrace() *ActionTrace {
	if len(callTrace.Actions) > 0 {
		return &callTrace.Actions[len(callTrace.Actions)-1]
	}
	return nil
}

// NewActionTrace creates new instance of type ActionTrace
func NewActionTrace(bHash common.Hash, bNumber big.Int, tHash common.Hash, tPos uint64, tType string) *ActionTrace {
	return &ActionTrace{
		BlockHash:           bHash,
		BlockNumber:         bNumber,
		TransactionHash:     tHash,
		TransactionPosition: tPos,
		TraceType:           tType,
		TraceAddress:        make([]uint32, 0),
		Result:              &TResult{},
	}
}

// NewActionTraceFromTrace creates new instance of type ActionTrace
// based on another trace
func NewActionTraceFromTrace(actionTrace *ActionTrace, tType string, traceAddress []uint32) *ActionTrace {
	trace := NewActionTrace(
		actionTrace.BlockHash,
		actionTrace.BlockNumber,
		actionTrace.TransactionHash,
		actionTrace.TransactionPosition,
		tType)
	trace.TraceAddress = traceAddress
	return trace
}

const (
	CALL         = "call"
	CREATE       = "create"
	SELFDESTRUCT = "suicide"
)

// ActionTrace represents single interaction with blockchain
type ActionTrace struct {
	childTraces  []*ActionTrace
	Subtraces    uint64   `json:"subtraces"`
	TraceAddress []uint32 `json:"traceAddress"`
	TraceType    string   `json:"type"`
	Action       TAction  `json:"action"`
	Result       *TResult `json:"result,omitempty"`
	Error        string   `json:"error,omitempty"`
	// Blockchain information
	BlockHash           common.Hash `json:"blockHash"`
	BlockNumber         big.Int     `json:"blockNumber"`
	TransactionHash     common.Hash `json:"transactionHash"`
	TransactionPosition uint64      `json:"transactionPosition"`
}

// NewTAction creates specific information about trace addresses.
func NewTAction(from, to *common.Address, gas uint64, data []byte, value hexutil.Big, callType *string) *TAction {
	action := TAction{
		From:     from,
		To:       to,
		Gas:      hexutil.Uint64(gas),
		Value:    value,
		CallType: callType,
	}
	if callType == nil { // CREATE* CALL
		action.Init = data
	} else {
		action.Input = data
	}
	return &action
}

// TAction represents the trace action model which from parity.
type TAction struct {
	CallType      *string         `json:"callType,omitempty"`
	From          *common.Address `json:"from"`
	To            *common.Address `json:"to,omitempty"`
	Value         hexutil.Big     `json:"value"`
	Gas           hexutil.Uint64  `json:"gas"`
	Init          hexutil.Bytes   `json:"init,omitempty"`
	Input         hexutil.Bytes   `json:"input,omitempty"`
	Address       *common.Address `json:"address,omitempty"`
	RefundAddress *common.Address `json:"refundAddress,omitempty"`
	Balance       *hexutil.Big    `json:"balance,omitempty"`
}

// TResult holds information related to result of the
// processed transaction.
type TResult struct {
	GasUsed   hexutil.Uint64  `json:"gasUsed"`
	Output    *hexutil.Bytes  `json:"output,omitempty" rlp:"nil"`
	Code      hexutil.Bytes   `json:"code,omitempty"`
	Address   *common.Address `json:"address,omitempty" rlp:"nil"`
	RetOffset uint64          `json:"-" rlp:"-"`
	RetSize   uint64          `json:"-" rlp:"-"`
}

// depthState is struct for having state of logs processing
type depthState struct {
	level  int
	create bool
}

// returns last state
func lastState(state []depthState) *depthState {
	return &state[len(state)-1]
}

// adds trace address and returns it
func addTraceAddress(traceAddress []uint32, depth int) []uint32 {
	index := depth - 1
	result := make([]uint32, len(traceAddress))
	copy(result, traceAddress)
	if len(result) <= index {
		result = append(result, 0)
	} else {
		result[index]++
	}
	return result
}

// removes trace address based on depth of process
func removeTraceAddressLevel(traceAddress []uint32, depth int) []uint32 {
	if len(traceAddress) > depth {
		result := make([]uint32, len(traceAddress))
		copy(result, traceAddress)

		result = result[:len(result)-1]
		return result
	}
	return traceAddress
}

// processLastTrace initiates final information distribution
// across result traces
func (callTrace *CallTrace) processLastTrace() {
	trace := &callTrace.Actions[len(callTrace.Actions)-1]
	callTrace.processTrace(trace)
}

// processTrace goes through all trace results and sets info
func (callTrace *CallTrace) processTrace(trace *ActionTrace) {
	trace.Subtraces = uint64(len(trace.childTraces))
	for _, childTrace := range trace.childTraces {
		// if CALL == trace.TraceType {
		// 	childTrace.Action.From = trace.Action.To
		// } else {
		// 	if trace.Result != nil {
		// 		childTrace.Action.From = trace.Result.Address
		// 	}
		// }

		if childTrace.Result != nil {
			if trace.Action.Gas > childTrace.Result.GasUsed {
				childTrace.Action.Gas = trace.Action.Gas - childTrace.Result.GasUsed
			} else {
				childTrace.Action.Gas = childTrace.Result.GasUsed
			}
		}
		if childTrace.TraceType == SELFDESTRUCT {
			childTrace.Action.Gas = 0
			childTrace.Action.From = nil
			childTrace.Result = nil
		}
		callTrace.AddTrace(childTrace)
		callTrace.processTrace(callTrace.lastTrace())
	}
}

// GetErrorTrace constructs filled error trace
func GetErrorTrace(blockHash common.Hash, blockNumber big.Int, to *common.Address, txHash common.Hash, index uint64, err error) *ActionTrace {

	var blockTrace *ActionTrace
	var txAction *TAction

	if to != nil {
		blockTrace = NewActionTrace(blockHash, blockNumber, txHash, index, "empty")
		txAction = NewTAction(&common.Address{}, to, 0, []byte{}, hexutil.Big{}, nil)
	} else {
		blockTrace = NewActionTrace(blockHash, blockNumber, txHash, index, "empty")
		txAction = NewTAction(&common.Address{}, nil, 0, []byte{}, hexutil.Big{}, nil)
	}
	blockTrace.Action = *txAction
	blockTrace.Result = nil
	if err != nil {
		blockTrace.Error = err.Error()
	} else {
		blockTrace.Error = "Reverted"
	}
	return blockTrace
}
