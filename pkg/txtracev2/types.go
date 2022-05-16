package txtracev2

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
	"github.com/ethereum/go-ethereum/rlp"
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

// InternalActions uses for store, simplifies structure to save space while compares with ActionTraceList
type InternalActionTraceList struct {
	Traces              []*InternalActionTrace
	BlockHash           common.Hash
	BlockNumber         *big.Int
	TransactionHash     common.Hash
	TransactionPosition uint64
}

// ToTraces convert InternalActionTraceLList to ActionTraceList
func (it *InternalActionTraceList) ToTraces() (traces ActionTraceList) {
	for _, interTrace := range it.Traces {
		value := big.NewInt(0)
		if interTrace.Action.Value != nil {
			value.Set(interTrace.Action.Value)
		}
		rpcTrace := &ActionTrace{
			Action: Action{
				Gas:   hexutil.Uint64(interTrace.Action.Gas),
				Value: (*hexutil.Big)(value),
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
			toTraceCreate(interTrace, rpcTrace)
		case CallTypeSuicide:
			rpcTrace.TraceType = "suicide"
			toTraceSuicide(interTrace, rpcTrace)
		default:
			rpcTrace.TraceType = "call"
			toTraceCall(interTrace, rpcTrace)
		}
		traces = append(traces, *rpcTrace)
	}
	return
}

// toTraceCreate handles crate sub action
func toTraceCreate(interTrace *InternalActionTrace, rpcTrace *ActionTrace) {
	init := hexutil.Bytes(interTrace.Action.Init)
	rpcTrace.Action.Init = &init
	rpcTrace.Action.Input = nil
	rpcTrace.Action.From = interTrace.Action.From
	if interTrace.Error != "" {
		rpcTrace.Error = interTrace.Error
		return
	}
	code := hexutil.Bytes(interTrace.Result.Code)
	rpcTrace.Result = &ActionResult{
		GasUsed: hexutil.Uint64(interTrace.Result.GasUsed),
		Code:    &code,
		Address: interTrace.Result.Address,
	}
}

// toTraceCall handles call sub action
func toTraceCall(interTrace *InternalActionTrace, rpcTrace *ActionTrace) {
	input := hexutil.Bytes(interTrace.Action.Input)
	rpcTrace.Action.Input = &input
	rpcTrace.Action.Init = nil
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
	rpcTrace.Result = &ActionResult{
		GasUsed: hexutil.Uint64(interTrace.Result.GasUsed),
		Output:  &output,
	}
}

// toTraceSuicide handles selfdestruct sub action
func toTraceSuicide(interTrace *InternalActionTrace, rpcTrace *ActionTrace) {
	// suicide has no init/input
	rpcTrace.Action.Init = nil
	rpcTrace.Action.Input = nil
	rpcTrace.Action.Address = interTrace.Action.Address
	rpcTrace.Action.RefundAddress = interTrace.Action.RefundAddress
	rpcTrace.Action.Value = nil
	balance := big.NewInt(0)
	if interTrace.Action.Balance != nil {
		balance.Set(interTrace.Action.Balance)
	}
	rpcTrace.Action.Balance = (*hexutil.Big)(balance)
}

type Action struct {
	CallType      *string         `json:"callType,omitempty"` // for CALL, CALL_CODE, DELEGATE_CALL, STATIC_CALL
	From          *common.Address `json:"from"`
	To            *common.Address `json:"to,omitempty"`
	Value         *hexutil.Big    `json:"value"`
	Gas           hexutil.Uint64  `json:"gas"`
	Init          *hexutil.Bytes  `json:"init,omitempty"`          // for CREATE
	Input         *hexutil.Bytes  `json:"input,omitempty"`         // for CALL, CALL_CODE, DELEGATE_CALL, STATIC_CALL
	Address       *common.Address `json:"address,omitempty"`       // for SELFDESTRUCT
	RefundAddress *common.Address `json:"refundAddress,omitempty"` // for SELFDESTRUCT
	Balance       *hexutil.Big    `json:"balance,omitempty"`       // for SELFDESTRUCT
}

type ActionResult struct {
	GasUsed hexutil.Uint64  `json:"gasUsed"`
	Output  *hexutil.Bytes  `json:"output,omitempty"`  // for CALL, CALL_CODE, DELEGATE_CALL, STATIC_CALL
	Code    *hexutil.Bytes  `json:"code,omitempty"`    // for CREATE
	Address *common.Address `json:"address,omitempty"` // for CREATE
}

// ActionTrace use for jsonrpc
type ActionTrace struct {
	Action              Action        `json:"action"`
	BlockHash           common.Hash   `json:"blockHash"`
	BlockNumber         *big.Int      `json:"blockNumber"`
	Result              *ActionResult `json:"result,omitempty"`
	Error               string        `json:"error,omitempty"`
	Subtraces           uint32        `json:"subtraces"`
	TraceAddress        []uint32      `json:"traceAddress"`
	TransactionHash     common.Hash   `json:"transactionHash"`
	TransactionPosition uint64        `json:"transactionPosition"`
	TraceType           string        `json:"type"`
}

type ActionTraceList []ActionTrace

func (rl *ActionTraceList) DecodeRLP(s *rlp.Stream) error {
	internalActionTraces := InternalActionTraceList{}
	if err := s.Decode(&internalActionTraces); err != nil {
		return err
	}
	*rl = append(*rl, internalActionTraces.ToTraces()...)
	return nil
}
