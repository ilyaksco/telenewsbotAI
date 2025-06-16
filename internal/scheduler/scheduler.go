package scheduler

import (
	"log"
	"time"

	"github.com/go-co-op/gocron/v2"
)

type Scheduler struct {
	instance gocron.Scheduler
}

func NewScheduler() (*Scheduler, error) {
	s, err := gocron.NewScheduler()
	if err != nil {
		return nil, err
	}
	return &Scheduler{instance: s}, nil
}

func (s *Scheduler) AddJob(tag string, interval time.Duration, job func()) {
	_, err := s.instance.NewJob(
		gocron.DurationJob(interval),
		gocron.NewTask(job),
		gocron.WithTags(tag),
	)
	if err != nil {
		log.Printf("Error adding job with tag %s to scheduler: %v", tag, err)
	}
}

func (s *Scheduler) RemoveJobByTag(tag string) {
	s.instance.RemoveByTags(tag)
}

func (s *Scheduler) Start() {
	s.instance.Start()
	log.Println("Scheduler started")
}