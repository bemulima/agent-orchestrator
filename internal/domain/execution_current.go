package domain

// Accept only the newest still-active durable attempt, not merely a valid
// response from a process that was once current.
func TaskExecutionCurrent(id string, number int, taskID string, attempts []TaskAttempt) bool {
	var latest *TaskAttempt
	for i := range attempts {
		a := &attempts[i]
		if a.TaskID == taskID && (latest == nil || a.AttemptNumber > latest.AttemptNumber) {
			latest = a
		}
	}
	if latest == nil || latest.ID != id || latest.AttemptNumber != number {
		return false
	}
	return latest.Status == TaskAttemptStatusRunning || latest.Status == TaskAttemptStatusVerification || latest.Status == TaskAttemptStatusReview
}
func ShardExecutionCurrent(id string, number int, shardID string, attempts []ShardAttempt) bool {
	var latest *ShardAttempt
	for i := range attempts {
		a := &attempts[i]
		if a.ShardID == shardID && (latest == nil || a.AttemptNumber > latest.AttemptNumber) {
			latest = a
		}
	}
	return latest != nil && latest.ID == id && latest.AttemptNumber == number && latest.Status == ShardAttemptRunning
}
