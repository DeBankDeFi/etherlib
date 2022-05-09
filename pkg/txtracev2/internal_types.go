package txtracev2

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

const (
	CallTypeCreate uint8 = iota
	CallTypeCall
	CallTypeCallCode
	CallTypeDelegateCall
	CallTypeStaticCall
	CallTypeSuicide
)

var (
	Call         string = "call"
	CallCode     string = "callcode"
	DelegateCall string = "delegatecall"
	StaticCall   string = "staticcall"
)

type InternalAction struct {
	CallType      uint8
	From          *common.Address `rlp:"nil"` // for SELFDESTRUCT nil is possible
	To            *common.Address `rlp:"nil"`
	Value         *big.Int        `rlp:"nil"`
	Gas           uint64
	Init          []byte          // for CREATE
	Input         []byte          // for CALL, CALL_CODE, DELEGATE_CALL, STATIC_CALL
	Address       *common.Address `rlp:"nil"` // for SELFDESTRUCT, CREATE(internal)
	RefundAddress *common.Address `rlp:"nil"` // for SELFDESTRUCT
	Balance       *big.Int        `rlp:"nil"` // for SELFDESTRUCT
}

type InternalTraceActionResult struct {
	GasUsed uint64
	Output  []byte          // for CALL, CALL_CODE, DELEGATE_CALL, STATIC_CALL
	Code    []byte          // for CREATE
	Address *common.Address `rlp:"nil"` // for CREATE
}

type InternalActionTrace struct {
	Action       InternalAction
	Result       *InternalTraceActionResult `rlp:"nil"`
	Error        string
	TraceAddress []uint32
	Subtraces    uint32
}

// InternalActions uses for store, simplifies structure to save space while compares with []RpcActionTrace
type InternalActionTraces struct {
	Traces              []InternalActionTrace
	BlockHash           common.Hash
	BlockNumber         *big.Int
	TransactionHash     common.Hash
	TransactionPosition uint64
}

// ToRpcTraces convert InternalActionTraces to RpcActionTraces
func (it *InternalActionTraces) ToRpcTraces() (traces []RpcActionTrace) {
	for _, interTrace := range it.Traces {
		value := big.NewInt(0)
		if interTrace.Action.Value != nil {
			value.Set(interTrace.Action.Value)
		}
		rpcTrace := &RpcActionTrace{
			Action: RpcAction{
				Gas:   hexutil.Uint64(interTrace.Action.Gas),
				Value: (*hexutil.Big)(value),
				Input: interTrace.Action.Input,
				Init:  interTrace.Action.Init,
			},
			BlockHash:           it.BlockHash,
			BlockNumber:         it.BlockNumber,
			Subtraces:           interTrace.Subtraces,
			TraceAddress:        interTrace.TraceAddress,
			TransactionHash:     it.TransactionHash,
			TransactionPosition: it.TransactionPosition,
		}
		if rpcTrace.TraceAddress == nil {
			rpcTrace.TraceAddress = make([]uint32, 0)
		}
		switch interTrace.Action.CallType {
		case CallTypeCreate:
			rpcTrace.TraceType = "create"
			toRpcTraceCreate(&interTrace, rpcTrace)
		case CallTypeSuicide:
			rpcTrace.TraceType = "suicide"
			toRpcTraceSuicide(&interTrace, rpcTrace)
		default:
			rpcTrace.TraceType = "call"
			toRpcTraceCall(&interTrace, rpcTrace)
		}
		traces = append(traces, *rpcTrace)
	}
	return
}

// toRpcTraceCreate handles crate sub action
func toRpcTraceCreate(interTrace *InternalActionTrace, rpcTrace *RpcActionTrace) {
	rpcTrace.Action.From = interTrace.Action.From
	if interTrace.Error != "" {
		rpcTrace.Error = interTrace.Error
		return
	}
	code := hexutil.Bytes(interTrace.Result.Code)
	rpcTrace.Result = &RpcActionResult{
		GasUsed: hexutil.Uint64(interTrace.Result.GasUsed),
		Code:    &code,
		Address: interTrace.Result.Address,
	}
}

// toRpcTraceCall handles call sub action
func toRpcTraceCall(interTrace *InternalActionTrace, rpcTrace *RpcActionTrace) {
	switch interTrace.Action.CallType {
	case CallTypeCall:
		rpcTrace.Action.CallType = &Call
	case CallTypeCallCode:
		rpcTrace.Action.CallType = &CallCode
	case CallTypeDelegateCall:
		rpcTrace.Action.CallType = &DelegateCall
	case CallTypeStaticCall:
		rpcTrace.Action.CallType = &StaticCall
	default:
		rpcTrace.Action.CallType = &Call
	}
	rpcTrace.Action.From = interTrace.Action.From
	rpcTrace.Action.To = interTrace.Action.To
	if interTrace.Error != "" {
		rpcTrace.Error = interTrace.Error
		return
	}
	output := hexutil.Bytes(interTrace.Result.Output)
	rpcTrace.Result = &RpcActionResult{
		GasUsed: hexutil.Uint64(interTrace.Result.GasUsed),
		Output:  &output,
	}
}

// toRpcTraceSuicide handles selfdestruct sub action
func toRpcTraceSuicide(interTrace *InternalActionTrace, rpcTrace *RpcActionTrace) {
	rpcTrace.Action.Address = interTrace.Action.Address
	rpcTrace.Action.RefundAddress = interTrace.Action.RefundAddress
	rpcTrace.Action.Value = nil
	balance := big.NewInt(0)
	if interTrace.Action.Balance != nil {
		balance.Set(interTrace.Action.Balance)
	}
	rpcTrace.Action.Balance = (*hexutil.Big)(balance)
}
