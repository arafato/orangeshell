package wrangler

// ParallelTailTarget identifies a worker to tail.
type ParallelTailTarget struct {
	ScriptName string
	URL        string // workers.dev URL (may be empty)
}

// ParallelTailStartMsg requests the app to start parallel tailing for an env.
type ParallelTailStartMsg struct {
	EnvName string
	Scripts []ParallelTailTarget
}

// ParallelTailExitMsg requests the app to stop all parallel tail sessions.
type ParallelTailExitMsg struct{}
