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

func (s *Scheduler) AddJob(interval time.Duration, job func()) {
	_, err := s.instance.NewJob(
		gocron.DurationJob(interval),
		gocron.NewTask(job),
	)
	if err != nil {
		log.Printf("Error adding job to scheduler: %v", err)
	}
}

func (s *Scheduler) Start() {
	s.instance.Start()
	log.Println("Scheduler started")
}