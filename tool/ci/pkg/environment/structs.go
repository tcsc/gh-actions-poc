package environment

/*
   Below are struct definitions used to transform pull request and review
   events (represented as a json object) into Golang structs. The way these objects are
   structured are different, therefore separate structs for each event are needed
   to unmarshal appropiately.
*/

type NewPullRequest struct {
	PullRequest Num `json:"pull_request"`
}

type Num struct {
	Number int `json:"number"`
}

type PRComment struct {
	Comment Comment `json:"issue"`
}

// Comment contains information amount the pull request comment.
type Comment struct {
	Number Num `json:"number"`
}

// action represents the current action
type action struct {
	Action string `json:"action"`
}
