package gasfeesvc

import "math"

type EstimatedGasFee struct {
	MaxPriorityFeePerGas float64 `json:"maxPriorityFeePerGas"`
	MaxFeePerGas         float64 `json:"maxFeePerGas"`
}

type SuggestedGasFees struct {
	BaseBlock                  int64                       `json:"baseBlock"`
	NextBaseFee                float64                     `json:"nextBaseFee"`
	GasUsedRatio               []float64                   `json:"gasUsedRatio"`
	HistoricalBaseFees         []float64                   `json:"historicalBaseFees,omitempty"`
	HistoricalRewards          []float64                   `json:"historicalRewards,omitempty"`
	RegulatedHistoricalRewards []float64                   `json:"regulatedHistoricalRewards,omitempty"`
	StdDevThreshold            float64                     `json:"stdDevThreshold,omitempty"`
	PredictMode                string                      `json:"predictMode,omitempty"`
	EstimatedGasFees           map[string]*EstimatedGasFee `json:"estimatedGasFees"`
}

// round rounds a float64 to the specified number of decimal places.
func round9(val float64) float64 {
	precision := 9
	ratio := math.Pow(10, float64(precision))
	return math.Round(val*ratio) / ratio
}
