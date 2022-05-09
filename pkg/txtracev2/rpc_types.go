package txtracev2

import (
	"math/big"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/common/hexutil"
)

type RpcAction struct {
	CallType      *string         `json:"callType,omitempty"` // for CALL, CALL_CODE, DELEGATE_CALL, STATIC_CALL
	From          *common.Address `json:"from"`
	To            *common.Address `json:"to,omitempty"`
	Value         *hexutil.Big    `json:"value"`
	Gas           hexutil.Uint64  `json:"gas"`
	Init          hexutil.Bytes   `json:"init,omitempty"`          // for CREATE
	Input         hexutil.Bytes   `json:"input,omitempty"`         // for CALL, CALL_CODE, DELEGATE_CALL, STATIC_CALL
	Address       *common.Address `json:"address,omitempty"`       // for SELFDESTRUCT
	RefundAddress *common.Address `json:"refundAddress,omitempty"` // for SELFDESTRUCT
	Balance       *hexutil.Big    `json:"balance,omitempty"`       // for SELFDESTRUCT
}

type RpcActionResult struct {
	GasUsed hexutil.Uint64  `json:"gasUsed"`
	Output  *hexutil.Bytes  `json:"output,omitempty"`  // for CALL, CALL_CODE, DELEGATE_CALL, STATIC_CALL
	Code    *hexutil.Bytes  `json:"code,omitempty"`    // for CREATE
	Address *common.Address `json:"address,omitempty"` // for CREATE
}

// RpcActionTrace use for jsonrpc
type RpcActionTrace struct {
	Action              RpcAction        `json:"action"`
	BlockHash           common.Hash      `json:"blockHash"`
	BlockNumber         *big.Int         `json:"blockNumber"`
	Result              *RpcActionResult `json:"result,omitempty"`
	Error               string           `json:"error,omitempty"`
	Subtraces           uint32           `json:"subtraces"`
	TraceAddress        []uint32         `json:"traceAddress"`
	TransactionHash     common.Hash      `json:"transactionHash"`
	TransactionPosition uint64           `json:"transactionPosition"`
	TraceType           string           `json:"type"`
}
