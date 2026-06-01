package main

import "time"

type ttlVariant struct {
	Name    string
	Seconds *int64
}

func (v ttlVariant) SecondsValue() any {
	if v.Seconds == nil {
		return nil
	}
	return *v.Seconds
}

func pickTTLVariant(rng interface{ IntN(int) int }, visibilityTimeout time.Duration) ttlVariant {
	short := int64(1)

	mediumSeconds := int64((visibilityTimeout + 2*time.Second) / time.Second)
	if mediumSeconds <= 1 {
		mediumSeconds = 2
	}

	longSeconds := int64((visibilityTimeout*2 + 4*time.Second) / time.Second)
	if longSeconds <= mediumSeconds {
		longSeconds = mediumSeconds + 2
	}

	variants := []ttlVariant{
		{Name: "none", Seconds: nil},
		{Name: "short", Seconds: &short},
		{Name: "medium", Seconds: &mediumSeconds},
		{Name: "long", Seconds: &longSeconds},
	}

	return variants[rng.IntN(len(variants))]
}
