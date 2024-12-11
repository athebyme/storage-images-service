package types

import "time"

type ProcessStatus struct {
	ProductID int
	Status    string
	Timestamp time.Time
}
