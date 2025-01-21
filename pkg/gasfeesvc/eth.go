package gasfeesvc

import (
	"context"
	"math"
	"math/big"
	"sort"

	"github.com/ethereum/go-ethereum/eth"
	"github.com/ethereum/go-ethereum/rpc"
	"github.com/gonum/stat"
)

type estimatedGasFee struct {
	MaxPriorityFeePerGas float64 `json:"maxPriorityFeePerGas"`
	MaxFeePerGas         float64 `json:"maxFeePerGas"`
}

type suggestedGasFees struct {
	BaseBlock                  int64                       `json:"baseBlock"`
	NextBaseFee                float64                     `json:"nextBaseFee"`
	GasUsedRatio               []float64                   `json:"gasUsedRatio"`
	HistoricalBaseFees         []float64                   `json:"historicalBaseFees,omitempty"`
	HistoricalRewards          []float64                   `json:"historicalRewards,omitempty"`
	RegulatedHistoricalRewards []float64                   `json:"regulatedHistoricalRewards,omitempty"`
	StdDevThreshold            float64                     `json:"stdDevThreshold,omitempty"`
	EstimatedGasFees           map[string]*estimatedGasFee `json:"estimatedGasFees"`
}

// PreExecAPI provides pre exec info for rpc
type GasFeeAPI struct {
	e *eth.Ethereum
}

func NewGasFeeAPI(e *eth.Ethereum) *GasFeeAPI {
	return &GasFeeAPI{e: e}
}

func (api *GasFeeAPI) SuggestedGasFees(ctx context.Context, baseBlock *rpc.BlockNumber) (*suggestedGasFees, error) {
	// TODO make the following configurable
	// query the past 10 blocks
	blocks := 10
	stdDevThreshold := 1.0
	baseFeeIncreateRatio := []float64{1.0, 1.45, 2.35} // metamask is: 1, 1.43, 2.3
	tipFeePercentiles := []float64{0.1, 0.5, 0.9}
	levels := []string{"normal", "fast", "instant"}

	// firstly we get all percentiles, we will do preprocessing on the returned data and pickup 3 percentiles as the normal, fast, instant levels
	rewardPercentiles := []float64{}
	for i := range 100 {
		rewardPercentiles = append(rewardPercentiles, float64(i))
	}

	if baseBlock == nil {
		baseBlock = new(rpc.BlockNumber)
		*baseBlock = rpc.LatestBlockNumber
	}
	oldest, rewards, baseFees, gasUsedRatios, _, _, err := api.e.APIBackend.FeeHistory(ctx, uint64(blocks), *baseBlock, rewardPercentiles)
	if err != nil {
		return nil, err
	}

	// pre process the original data from the Oracle
	// 1. convert the original data unit "wei" to "gwei"
	// 2. remove the exceptional rewards that deviate too much from the mean
	results := &suggestedGasFees{
		BaseBlock:        oldest.Int64() + int64(blocks) - 1,
		GasUsedRatio:     gasUsedRatios,
		StdDevThreshold:  stdDevThreshold,
		EstimatedGasFees: make(map[string]*estimatedGasFee, 3),
	}
	for _, baseFee := range baseFees {
		if bf, accuracy := new(big.Float).SetInt(baseFee).Float64(); accuracy == 0 {
			results.HistoricalBaseFees = append(results.HistoricalBaseFees, bf/1_000_000_000)
			results.NextBaseFee = bf / 1_000_000_000 // set the next block's base fee here too
		}
	}
	for _, rewardsIn1Blk := range rewards {
		for _, txReward := range rewardsIn1Blk {
			if rwd, accuracy := new(big.Float).SetInt(txReward).Float64(); accuracy == 0 {
				results.HistoricalRewards = append(results.HistoricalRewards, rwd/1_000_000_000)
			}
		}
	}

	// remove the rewards that 1x from the Standard Deviation
	mean, stdDev := stat.MeanStdDev(results.HistoricalRewards, nil)
	regulated := []float64{}
	for _, num := range results.HistoricalRewards {
		if math.Abs(num-mean) <= stdDevThreshold*stdDev {
			regulated = append(regulated, num)
		}
	}
	sort.Float64s(regulated)
	results.RegulatedHistoricalRewards = regulated

	for i, level := range levels {
		percentile := tipFeePercentiles[i]
		baseFeeRatio := baseFeeIncreateRatio[i]

		idx := int(percentile * float64(len(regulated)))
		tip := regulated[idx]

		results.EstimatedGasFees[level] = &estimatedGasFee{
			MaxPriorityFeePerGas: tip,
			MaxFeePerGas:         results.NextBaseFee*baseFeeRatio + tip,
		}
	}
	return results, nil
}
