package environment

/*
   Below are struct definitions used to transform pull request and review
   events (represented as a json object) into Golang structs. The way these objects are
   structured are different, therefore separate structs for each event are needed
   to unmarshal appropiately.
*/

type PullRequest struct {
	PullRequest Number `json:"pull_request"`
}

type Number struct {
	Value int `json:"number"`
}

type PRComment struct {
	Comment Issue `json:"issue"`
}

// Issue contains information amount the pull request comment.
type Issue struct {
	Number Number
}

// action represents the current action
type action struct {
	Action string `json:"action"`
}
