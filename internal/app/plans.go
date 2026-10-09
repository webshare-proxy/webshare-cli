package app

import (
	"context"

	webshare "github.com/webshare-proxy/webshare-go"
)

// ListPlans returns the account's active plans, plus the cancelled ones when
// includeCancelled is set.
func ListPlans(ctx context.Context, client *webshare.Client, includeCancelled bool) ([]webshare.Plan, error) {
	var plans []webshare.Plan
	for plan, err := range client.Plans.ListAll(ctx, webshare.PlanListParams{}) {
		if err != nil {
			return nil, err
		}
		if !includeCancelled && plan.Status != webshare.PlanActive {
			continue
		}
		plans = append(plans, plan)
	}
	return plans, nil
}
