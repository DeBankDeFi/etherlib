package txtracev2

import (
	"context"
	"math/big"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/log"
	"github.com/ethereum/go-ethereum/rlp"
)

var _ vm.EVMLogger = (*OeTracer)(nil)

type OeTracer struct {
	store        Store
	traces       []*InternalActionTrace
	traceStack   []*InternalActionTrace
	outPutTraces InternalActionTraces
}

func NewOeTracer(db Store, blockHash common.Hash, blockNumber *big.Int, transactionHash common.Hash, transactionPosition uint64) *OeTracer {
	return &OeTracer{
		store: db,
		outPutTraces: InternalActionTraces{
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
		Init:     input,
		Address:  &address,
	}
	internalTrace := &InternalActionTrace{
		Action: action,
	}
	if len(ot.traceStack) > 0 {
		internalTrace.TraceAddress = ot.traceStack[len(ot.traceStack)-1].TraceAddress
		internalTrace.TraceAddress = append(internalTrace.TraceAddress, ot.traceStack[len(ot.traceStack)-1].Subtraces)
		ot.traceStack[len(ot.traceStack)-1].Subtraces++
	}
	ot.traces = append(ot.traces, internalTrace)
	ot.traceStack = append(ot.traceStack, internalTrace)
}

// captureExit handles CREATE/CREATE2 exit
func (ot *OeTracer) createExit(internalTrace *InternalActionTrace, output []byte, gasUsed uint64, err error) {
	if err != nil {
		internalTrace.Error = err.Error()
		internalTrace.Result = nil
	} else {
		internalTrace.Result = &InternalTraceActionResult{
			GasUsed: gasUsed,
			Address: internalTrace.Action.Address,
			Code:    output,
		}
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
		Input:    input,
	}
	internalTrace := &InternalActionTrace{
		Action: action,
	}
	if len(ot.traceStack) > 0 {
		internalTrace.TraceAddress = ot.traceStack[len(ot.traceStack)-1].TraceAddress
		internalTrace.TraceAddress = append(internalTrace.TraceAddress, ot.traceStack[len(ot.traceStack)-1].Subtraces)
		ot.traceStack[len(ot.traceStack)-1].Subtraces++
	}
	ot.traces = append(ot.traces, internalTrace)
	ot.traceStack = append(ot.traceStack, internalTrace)
}

// callExit handles CALL, CALL_CODE, DELEGATE_CALL, STATIC_CALL op exit
func (ot *OeTracer) callExit(internalTrace *InternalActionTrace, output []byte, gasUsed uint64, err error) {
	if err != nil {
		internalTrace.Error = err.Error()
		internalTrace.Result = nil
	} else {
		internalTrace.Result = &InternalTraceActionResult{
			GasUsed: gasUsed,
			Output:  output,
		}
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
		Action: action,
	}
	if len(ot.traceStack) > 0 {
		internalTrace.TraceAddress = ot.traceStack[len(ot.traceStack)-1].TraceAddress
		internalTrace.TraceAddress = append(internalTrace.TraceAddress, ot.traceStack[len(ot.traceStack)-1].Subtraces)
		ot.traceStack[len(ot.traceStack)-1].Subtraces++
	}
	ot.traces = append(ot.traces, internalTrace)
	ot.traceStack = append(ot.traceStack, internalTrace)
}

// suicideExit handles SELFDESTRUCT op exit
func (ot *OeTracer) suicideExit(internalTrace *InternalActionTrace, output []byte, gasUsed uint64, err error) {
	if err != nil {
		internalTrace.Error = err.Error()
		internalTrace.Result = nil
	}
}

// CaptureStart handle top call/create start
func (ot *OeTracer) CaptureStart(env *vm.EVM, from common.Address, to common.Address, create bool, input []byte, gas uint64, value *big.Int) {
	if create {
		ot.createEnter(from, to, input, gas, value)
	} else {
		ot.callEnter(CallTypeCall, from, to, input, gas, value)
	}
}

// CaptureEnd handle top call/create end
func (ot *OeTracer) CaptureEnd(output []byte, gasUsed uint64, t time.Duration, err error) {
	internalTrace := ot.traceStack[len(ot.traceStack)-1]
	ot.traceStack = ot.traceStack[:len(ot.traceStack)-1]
	if internalTrace.Action.CallType == CallTypeCreate {
		ot.createExit(internalTrace, output, gasUsed, err)
	} else {
		ot.callExit(internalTrace, output, gasUsed, err)
	}
	// finish
	for _, trace := range ot.traces {
		ot.outPutTraces.Traces = append(ot.outPutTraces.Traces, *trace)
	}
}

// CaptureEnter handle sub call/create/suide start
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

// CaptureExit handle sub call/create/suide end
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

// CaptureState do nothing
func (ot *OeTracer) CaptureState(pc uint64, op vm.OpCode, gas, cost uint64, scope *vm.ScopeContext, rData []byte, depth int, err error) {
}

// CaptureFault do nothing
func (ot *OeTracer) CaptureFault(pc uint64, op vm.OpCode, gas, cost uint64, scope *vm.ScopeContext, depth int, err error) {
}

// GetInternalTraces return Inter ActionTraces after evm runtime completed, then PersistTrace will store it to db
// If you want to return traces to clent,  call .ToRpcTraces to convert []RpcActionTrace or call GetRpcTraces directly
func (ot *OeTracer) GetInternalTraces() *InternalActionTraces {
	return &ot.outPutTraces
}

// GetRpcTraces return []RpcActionTrace for jsonrpc call
func (ot *OeTracer) GetRpcTraces() []RpcActionTrace {
	return ot.outPutTraces.ToRpcTraces()
}

// PersistTrace save traced tx result to underlying k-v store.
func (ot *OeTracer) PersistTrace() {
	if ot.store != nil {
		tracesBytes, err := rlp.EncodeToBytes(ot.GetInternalTraces())
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
