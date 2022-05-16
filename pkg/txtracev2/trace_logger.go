package txtracev2

import (
	"context"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/params"
	"github.com/ethereum/go-ethereum/rlp"
)

var _ vm.EVMLogger = (*OeTracer)(nil)

var emptyCodeHash = crypto.Keccak256Hash(nil)

type OeTracer struct {
	store        Store
	traceStack   []*InternalActionTrace
	outPutTraces InternalActionTraceList
	env          *vm.EVM
}

func NewOeTracer(db Store, blockHash common.Hash, blockNumber *big.Int, transactionHash common.Hash, transactionPosition uint64) *OeTracer {
	return &OeTracer{
		store: db,
		outPutTraces: InternalActionTraceList{
			BlockHash:           blockHash,
			BlockNumber:         blockNumber,
			TransactionHash:     transactionHash,
			TransactionPosition: transactionPosition,
		},
	}
}

// createEnter handles CREATE/CREATE2 op start
func (ot *OeTracer) createEnter(from common.Address, address common.Address, input []byte, gas uint64, value *big.Int) {
	action := InternalAction{
		CallType: CallTypeCreate,
		From:     &from,
		To:       nil,
		Value:    value,
		Gas:      gas,
		Init:     make([]byte, len(input)),
		Address:  &address,
	}
	copy(action.Init, input)
	internalTrace := &InternalActionTrace{
		Action:       action,
		TraceAddress: make([]uint32, 0),
	}
	if len(ot.traceStack) > 0 {
		internalTrace.TraceAddress = make([]uint32, len(ot.traceStack[len(ot.traceStack)-1].TraceAddress))
		copy(internalTrace.TraceAddress, ot.traceStack[len(ot.traceStack)-1].TraceAddress)
		internalTrace.TraceAddress = append(internalTrace.TraceAddress, ot.traceStack[len(ot.traceStack)-1].Subtraces)
		ot.traceStack[len(ot.traceStack)-1].Subtraces++
	}
	ot.outPutTraces.Traces = append(ot.outPutTraces.Traces, internalTrace)
	ot.traceStack = append(ot.traceStack, internalTrace)
}

// captureExit handles CREATE/CREATE2 op exit
func (ot *OeTracer) createExit(internalTrace *InternalActionTrace, output []byte, gasUsed uint64, err error) {
	if internalTrace.Error != "" {
		internalTrace.Result = nil
	} else if err != nil {
		internalTrace.Error = err.Error()
		internalTrace.Result = nil
	} else {
		internalTrace.Result = &InternalTraceActionResult{
			GasUsed: gasUsed,
			Address: internalTrace.Action.Address,
			Code:    make([]byte, len(output)),
		}
		copy(internalTrace.Result.Code, output)
	}
}

// callEnter handles CALL, CALL_CODE, DELEGATE_CALL, STATIC_CALL op start
func (ot *OeTracer) callEnter(callType uint8, from common.Address, to common.Address, input []byte, gas uint64, value *big.Int) {
	action := InternalAction{
		CallType: callType,
		From:     &from,
		To:       &to,
		Value:    value,
		Gas:      gas,
		Input:    make([]byte, len(input)),
	}
	copy(action.Input, input)
	internalTrace := &InternalActionTrace{
		Action:       action,
		TraceAddress: make([]uint32, 0),
	}
	if len(ot.traceStack) > 0 {
		internalTrace.TraceAddress = make([]uint32, len(ot.traceStack[len(ot.traceStack)-1].TraceAddress))
		copy(internalTrace.TraceAddress, ot.traceStack[len(ot.traceStack)-1].TraceAddress)
		internalTrace.TraceAddress = append(internalTrace.TraceAddress, ot.traceStack[len(ot.traceStack)-1].Subtraces)
		ot.traceStack[len(ot.traceStack)-1].Subtraces++
	}
	ot.outPutTraces.Traces = append(ot.outPutTraces.Traces, internalTrace)
	ot.traceStack = append(ot.traceStack, internalTrace)
}

// callExit handles CALL, CALL_CODE, DELEGATE_CALL, STATIC_CALL op exit
func (ot *OeTracer) callExit(internalTrace *InternalActionTrace, output []byte, gasUsed uint64, err error) {
	if internalTrace.Error != "" {
		internalTrace.Result = nil
	} else if err != nil {
		internalTrace.Error = err.Error()
		internalTrace.Result = nil
	} else {
		internalTrace.Result = &InternalTraceActionResult{
			GasUsed: gasUsed,
			Output:  make([]byte, len(output)),
		}
		copy(internalTrace.Result.Output, output)
	}
}

// suicideEnter handles SELFDESTRUCT op start
func (ot *OeTracer) suicideEnter(address common.Address, refundAddress common.Address, _ []byte, _ uint64, Balance *big.Int) {
	action := InternalAction{
		CallType:      CallTypeSuicide,
		Address:       &address,
		RefundAddress: &refundAddress,
		Balance:       Balance,
	}
	internalTrace := &InternalActionTrace{
		Action:       action,
		TraceAddress: make([]uint32, 0),
	}
	if len(ot.traceStack) > 0 {
		internalTrace.TraceAddress = make([]uint32, len(ot.traceStack[len(ot.traceStack)-1].TraceAddress))
		copy(internalTrace.TraceAddress, ot.traceStack[len(ot.traceStack)-1].TraceAddress)
		internalTrace.TraceAddress = append(internalTrace.TraceAddress, ot.traceStack[len(ot.traceStack)-1].Subtraces)
		ot.traceStack[len(ot.traceStack)-1].Subtraces++
	}
	ot.outPutTraces.Traces = append(ot.outPutTraces.Traces, internalTrace)
	ot.traceStack = append(ot.traceStack, internalTrace)
}

// suicideExit handles SELFDESTRUCT op exit
func (ot *OeTracer) suicideExit(internalTrace *InternalActionTrace, output []byte, gasUsed uint64, err error) {
	if internalTrace.Error != "" {
		internalTrace.Result = nil
	} else if err != nil {
		internalTrace.Error = err.Error()
		internalTrace.Result = nil
	}
}

// CaptureStart handles top call/create start
func (ot *OeTracer) CaptureStart(env *vm.EVM, from common.Address, to common.Address, create bool, input []byte, gas uint64, value *big.Int) {
	if create {
		ot.createEnter(from, to, input, gas, value)
	} else {
		ot.callEnter(CallTypeCall, from, to, input, gas, value)
	}
	ot.env = env
}

// CaptureEnd handles top call/create end
func (ot *OeTracer) CaptureEnd(output []byte, gasUsed uint64, t time.Duration, err error) {
	internalTrace := ot.traceStack[len(ot.traceStack)-1]
	ot.traceStack = ot.traceStack[:len(ot.traceStack)-1]
	if internalTrace.Action.CallType == CallTypeCreate {
		ot.createExit(internalTrace, output, gasUsed, err)
	} else {
		ot.callExit(internalTrace, output, gasUsed, err)
	}
}

// CaptureEnter handles sub call/create/suide start
func (ot *OeTracer) CaptureEnter(typ vm.OpCode, from common.Address, to common.Address, input []byte, gas uint64, value *big.Int) {
	switch typ {
	case vm.CREATE, vm.CREATE2:
		ot.createEnter(from, to, input, gas, value)
	case vm.CALL:
		ot.callEnter(CallTypeCall, from, to, input, gas, value)
	case vm.CALLCODE:
		ot.callEnter(CallTypeCallCode, from, to, input, gas, value)
	case vm.DELEGATECALL:
		ot.callEnter(CallTypeDelegateCall, from, to, input, gas, value)
	case vm.STATICCALL:
		ot.callEnter(CallTypeStaticCall, from, to, input, gas, value)
	case vm.SELFDESTRUCT:
		ot.suicideEnter(from, to, input, gas, value)
	}
}

// CaptureExit handles sub call/create/suide end
func (ot *OeTracer) CaptureExit(output []byte, gasUsed uint64, err error) {
	internalTrace := ot.traceStack[len(ot.traceStack)-1]
	ot.traceStack = ot.traceStack[:len(ot.traceStack)-1]
	switch internalTrace.Action.CallType {
	case CallTypeCreate:
		ot.createExit(internalTrace, output, gasUsed, err)
	case CallTypeCall, CallTypeCallCode, CallTypeDelegateCall, CallTypeStaticCall:
		ot.callExit(internalTrace, output, gasUsed, err)
	case CallTypeSuicide:
		ot.suicideExit(internalTrace, output, gasUsed, err)
	}
}

// CaptureState handles some pre-processing errors, CaptureEnter and CaptureExit will not be called on this case
func (ot *OeTracer) CaptureState(pc uint64, op vm.OpCode, gas, cost uint64, scope *vm.ScopeContext, rData []byte, depth int, err error) {
	switch op {
	case vm.CREATE, vm.CREATE2:
		value := scope.Stack.Back(0)
		bigVal := big.NewInt(0)
		if !value.IsZero() {
			bigVal = value.ToBig()
		}
		if err != nil {
			ot.createPreProcessFailed(op, scope, gas, bigVal, err)
			return
		}
		if err = ot.checkDepthAboveLitmit(depth); err != nil {
			ot.createPreProcessFailed(op, scope, gas, bigVal, err)
			return
		}
		if err = ot.checkCanTransfer(scope.Contract.Address(), bigVal); err != nil {
			ot.createPreProcessFailed(op, scope, gas, bigVal, err)
			return
		}
		if err = ot.checkNonceMatch(scope.Contract.Address()); err != nil {
			ot.createPreProcessFailed(op, scope, gas, bigVal, err)
			return
		}
		if err = ot.checkContractNotExist(scope.Contract.Address()); err != nil {
			ot.createPreProcessFailed(op, scope, gas, bigVal, err)
			return
		}
	case vm.CALL, vm.CALLCODE:
		value := scope.Stack.Back(2)
		bigVal := big.NewInt(0)
		if !value.IsZero() {
			bigVal = value.ToBig()
		}
		if err = ot.checkDepthAboveLitmit(depth); err != nil {
			ot.callPreProcessFailed(op, scope, gas, bigVal, err)
			return
		}
		if err != nil {
			ot.callPreProcessFailed(op, scope, gas, bigVal, err)
			return
		}
		if err = ot.checkCanTransfer(scope.Contract.Address(), bigVal); err != nil {
			ot.callPreProcessFailed(op, scope, gas, bigVal, err)
			return
		}
	case vm.DELEGATECALL, vm.STATICCALL:
		if err != nil {
			ot.callPreProcessFailed(op, scope, gas, nil, err)
			return
		}
		if err = ot.checkDepthAboveLitmit(depth); err != nil {
			ot.callPreProcessFailed(op, scope, gas, nil, err)
			return
		}
	case vm.REVERT:
		ot.traceStack[len(ot.traceStack)-1].Error = "execution reverted"
	}
}

func (ot *OeTracer) createPreProcessFailed(op vm.OpCode, scope *vm.ScopeContext, gas uint64, value *big.Int, err error) {
	offset, size := scope.Stack.Back(1), scope.Stack.Back(2)
	input := scope.Memory.GetCopy(int64(offset.Uint64()), int64(size.Uint64()))
	ot.CaptureEnter(op, scope.Contract.Address(), common.Address{}, input, gas, value)
	ot.CaptureExit(nil, 0, err)
}

func (ot *OeTracer) callPreProcessFailed(op vm.OpCode, scope *vm.ScopeContext, gas uint64, value *big.Int, err error) {
	var input []byte
	addr := scope.Stack.Back(1)
	if op == vm.CALL || op == vm.CALLCODE {
		offset, size := scope.Stack.Back(3), scope.Stack.Back(4)
		input = scope.Memory.GetCopy(int64(offset.Uint64()), int64(size.Uint64()))
	} else {
		offset, size := scope.Stack.Back(2), scope.Stack.Back(3)
		input = scope.Memory.GetCopy(int64(offset.Uint64()), int64(size.Uint64()))
	}
	ot.CaptureEnter(op, scope.Contract.Address(), common.Address(addr.Bytes20()), input, gas, value)
	ot.CaptureExit(nil, 0, err)
}

// checkDepthAboveLitmit check if the depth is above the limit
func (ot *OeTracer) checkDepthAboveLitmit(depth int) error {
	if depth > int(params.CallCreateDepth) {
		return vm.ErrDepth
	}
	return nil
}

// checkCanTransfer check if the balance is enough to transfer
func (ot *OeTracer) checkCanTransfer(addr common.Address, value *big.Int) error {
	if value.Sign() != 0 && !ot.env.Context.CanTransfer(ot.env.StateDB, addr, value) {
		return vm.ErrInsufficientBalance
	}
	return nil
}

// checkNonceMatch check if the nonce is match
func (ot *OeTracer) checkNonceMatch(addr common.Address) error {
	nonce := ot.env.StateDB.GetNonce(addr)
	if nonce+1 < nonce {
		return vm.ErrNonceUintOverflow
	}
	return nil
}

// checkContractNotExist check if the contract is exist at the designated address
func (ot *OeTracer) checkContractNotExist(addr common.Address) error {
	contractHash := ot.env.StateDB.GetCodeHash(addr)
	if ot.env.StateDB.GetNonce(addr) != 0 || (contractHash != (common.Hash{}) && contractHash != emptyCodeHash) {
		return vm.ErrContractAddressCollision
	}
	return nil
}

// CaptureFault do nothing
func (ot *OeTracer) CaptureFault(pc uint64, op vm.OpCode, gas, cost uint64, scope *vm.ScopeContext, depth int, err error) {
}

// getInternalTraces return Inter ActionTraces after evm runtime completed, then PersistTrace will store it to db
// If you want to return traces to clent,  call .ToRpcTraces to convert ActionTraceList or call GetTraces directly
func (ot *OeTracer) getInternalTraces() *InternalActionTraceList {
	return &ot.outPutTraces
}

// GetTraces return ActionTraceList for jsonrpc call
func (ot *OeTracer) GetTraces() ActionTraceList {
	return ot.outPutTraces.ToTraces()
}

// PersistTrace save traced tx result to underlying k-v store.
func (ot *OeTracer) PersistTrace() {
	if ot.store != nil {
		tracesBytes, err := rlp.EncodeToBytes(ot.getInternalTraces())
		if err != nil {
			log.Error("Failed to encode tx trace", "txHash", ot.outPutTraces.TransactionHash.String(), "err", err.Error())
			return
		}
		if err := ot.store.WriteTxTrace(context.Background(), ot.outPutTraces.TransactionHash, tracesBytes); err != nil {
			log.Error("Failed to persist tx trace to database", "txHash", ot.outPutTraces.TransactionHash.String(), "err", err.Error())
			return
		}
	}
}
