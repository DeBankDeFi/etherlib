package txtracev2

import (
	"bytes"
	"context"
	"fmt"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/rlp"
)

// Store contains all the methods for tx-trace to interact with the underlying database.
type Store interface {
	// ReadTxTrace retrieve tracing result from underlying database.
	ReadTxTrace(ctx context.Context, txHash common.Hash) ([]byte, error)
	// WriteTxTrace write tracing result to underlying database.
	WriteTxTrace(ctx context.Context, txHash common.Hash, trace []byte) error
}

// ReadRpcTxTrace reads internal tx-trace from underlying database and decodes it to rpc-tx-trace.
func ReadRpcTxTrace(store Store, ctx context.Context, txHash common.Hash) ([]RpcActionTrace, error) {
	raw, err := store.ReadTxTrace(ctx, txHash)
	if err != nil {
		return nil, err
	}
	if bytes.Equal(raw, []byte{}) { // empty response
		return nil, fmt.Errorf("trace result of tx {%#v} not found in tracedb", txHash)
	}
	interTxs := new(InternalActionTraces)
	err = rlp.DecodeBytes(raw, interTxs)
	if err != nil {
		return nil, fmt.Errorf("failed to decode rlp traces: %v", err)
	}
	return interTxs.ToRpcTraces(), nil
}
