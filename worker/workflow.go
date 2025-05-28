package main

import (
	"time"

	"temporal-key-rotation/shared"

	"go.temporal.io/sdk/temporal"
	"go.temporal.io/sdk/workflow"
)

func ProcessPayloadWorkflow(ctx workflow.Context, p shared.Payload) error {
	logger := workflow.GetLogger(ctx)
	logger.Info("Workflow started", "ID", p.ID, "Name", p.Name, "Email", p.Email)

	ao := workflow.ActivityOptions{
		StartToCloseTimeout: time.Second * 30, // Increased timeout for database operations
		RetryPolicy: &temporal.RetryPolicy{
			InitialInterval:    time.Second * 2,
			BackoffCoefficient: 2.0,
			MaximumInterval:    time.Second * 30,
			MaximumAttempts:    3,
		},
	}
	ctx = workflow.WithActivityOptions(ctx, ao)

	// Execute the InsertPayload activity
	err := workflow.ExecuteActivity(ctx, "InsertPayload", p).Get(ctx, nil)
	if err != nil {
		logger.Error("Activity failed", "error", err)
		return err
	}

	logger.Info("Workflow completed successfully", "ID", p.ID)
	return nil
}
