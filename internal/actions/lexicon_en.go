package actions

// lexEN is the English (US) phrase table, same rules as lexRU. Every
// category is pinned by golden phrases in lexicon_test.go (ADR 153).
var lexEN = lexicon{
	promise: []string{
		`i'?ll`, `i will`, `we'?ll`, `we will`, `i'?m gonna`, `we'?re gonna`, `i'?m going to`, `we'?re going to`,
		`will (?:do|send|check|look|investigate|prepare|share|update|follow up|get back|ping|fix|review|come back|circle back|take a look|handle|sort it|dig in|get to it)`,
		`on it`, `let me (?:check|look|see|dig|find out|get back|confirm|ask)`, `in process`, `in progress`, `will do`,
		`working on it`, `i can do (?:it|that|this)`, `i can take (?:it|that|this)`, `leave it with me`,
		`on my list`, `circling back`,
	},
	request: []string{
		`please`, `pls`, `plz`, `can you`, `could you`, `would you`, `can u`, `could u`, `need`, `needs`,
		`let me know`, `lmk`, `give me`, `think about`, `send me`, `make sure`,
		`do you mind`, `would you mind`, `mind (?:taking|checking|sending|looking)`, `would be (?:great|nice|awesome) if`,
		`when you get a (?:chance|sec|minute)`, `any updates?`, `eta`, `following up`, `just following up`,
		`(?:gentle|friendly|quick) reminder`, `just checking in`, `keep me posted`, `fill me in`,
	},
	done: []string{
		`done`, `sent`, `shipped`, `fixed`, `merged`, `ready`, `deployed`, `here it is`, `here you go`,
		`attached`, `resolved`, `closed`, `pushed to prod`, `released`,
		`finished`, `completed`, `wrapped up`, `all set`, `taken care of`, `landed`, `(?:is|it'?s|now|went) live`,
		`updated`, `uploaded`, `lgtm`, `approved`, `good to go`,
	},
	ack: []string{
		`ok`, `okay`, `got it`, `on it`, `will do`, `will investigate`, `noted`, `sure`, `in process`, `in progress`,
		`sounds good`, `ack`, `roger`, `will check`, `on my radar`, `👍`, `:\+1:`, `:thumbsup:`,
	},
	absence: []string{
		`day off`, `sick leave`, `sick day`, `pto`, `vacation`, `out of office`, `ooo`, `out sick`, `off sick`,
		`on leave`, `parental leave`, `on holiday`,
	},
	thanks: []string{`thanks`, `thank you`, `thx`, `ty`, `tysm`, `tyvm`, `appreciate it`, `much appreciated`, `cheers`},
	urgent: []string{`asap`, `urgent`, `p0`, `blocker`, `blocking`, `critical`, `time[- ]sensitive`, `high priority`},
	polite: []string{`please`, `pls`, `plz`, `can you`, `could you`, `would you`, `can u`, `could u`},
	redirect: []string{
		`(?:ask|ping|reach out to|talk to|check with|contact|go to)\s+(?:the\s+)?(\S+)`,
	},
	self: []string{`me`, `us`, `myself`},
}
