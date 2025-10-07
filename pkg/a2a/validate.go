package a2a

import "errors"

func ValidateCreateTask(ct *CreateTask) error {
	if ct.TaskType == "" {
		return errors.New("task_type is required")
	}
	if len(ct.Input) == 0 {
		return errors.New("input is required")
	}
	return nil
}

func CanTransition(from, to TaskStatus) bool {
	switch from {
	case StatusPending:
		return to == StatusRunning || to == StatusSucceeded || to == StatusFailed
	case StatusRunning:
		return to == StatusSucceeded || to == StatusFailed
	case StatusSucceeded, StatusFailed:
		return false
	default:
		return false
	}
}
