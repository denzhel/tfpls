package onboard

import (
	"math/rand/v2"
	"regexp"
	"strconv"
)

var jokes = []string{
	"It is not DNS. There is no way it is DNS. It was DNS.",
	"A plan with nothing to destroy is the most beautiful sentence in infrastructure.",
	"Why are tofu engineers so calm? They always have a plan.",
	"The S in IaC stands for security.",
	"I asked my state file for some space. It said: Error acquiring the state lock.",
	"Infrastructure as code: because clicking around the console is not a disaster recovery plan.",
	"Tofu takes on the flavour of whatever cloud you put it in.",
	"Drift happens. That is why we check every morning.",
	"Real engineers apply on Friday. Wise ones comment /tofu apply on Monday.",
	"count = 0: deleting things without admitting it.",
	"A YAML file walks into a bar. The bartender asks: is that indentation intentional?",
	"My servers are not pets or cattle. They are tofu: cubed, soft and easy to replace.",
	"-target is like duct tape: it works, and now you have two problems.",
	"Rollback plan: git revert, then a long walk.",
	"Two applies walk into a state lock. One of them waits two minutes, then leaves.",
	"Some people count sheep. SREs count pending changes.",
	"Works on my machine? Fine, then we ship your laptop to production.",
	"The best time to pin your provider versions was yesterday. The second best is before tofu init -upgrade.",
	"The cloud is just someone else's computer. The state file is your diary about it.",
	"There are two hard things in infrastructure: naming things, state locks, and off-by-one errors in count.",
	"Why did the plan look so relaxed? Nothing to add, nothing to change, nothing to destroy.",
	"I would tell you a UDP joke, but you might not get it.",
	"ClickOps: changing production without leaving a trace. Until the drift check.",
	"Dev, staging, prod: three workspaces, one shared sense of dread.",
	"Immutable infrastructure: because 'I fixed it by hand' does not survive the next apply.",
	"Every apply is a leap of faith. A reviewed plan is the parachute.",
}

var seedRe = regexp.MustCompile(`^[0-9]{1,9}$`)

// pickJoke returns the index of the joke: the seed modulo the number of jokes for a seed of 1 to 9 digits (the same joke
// as the Bash version for the same seed), a random one otherwise.
func pickJoke(seed string) int {
	if seedRe.MatchString(seed) {
		n, _ := strconv.Atoi(seed)
		return n % len(jokes)
	}
	return rand.IntN(len(jokes))
}
