package main

import "testing"

// TestBroadBeerPatterns ensures broadened regex patterns detect gifting intent in varied forms.
func TestBroadBeerPatterns(t *testing.T) {
	bot := &MinimalSlackBot{}
	cases := []string{
		"🍺 <@U12345>",                              // original
		"🍻 <@U12345>",                              // original variant
		":beer: <@U12345>",                         // textual emoji before mention
		"<@U12345> 🍺",                              // mention-first single
		"<@U12345> 🍺🍺🍺",                          // mention-first cluster
		"<@U12345> :beer:",                          // mention-first textual emoji
		"<@U12345> beer",                            // mention-first keyword
		"Give <@U12345> 🍺",                         // verb first
		"GIVES <@U12345> 3 beers",                   // verb + quantity + plural
		"giving <@U12345> :beers:",                 // verb + textual plural
		"gift <@U12345> beer",                       // gift verb
		"gifting <@U12345> 2 :beer:",                // gifting verb + quantity
		"<@U12345> gives 4 🍺🍺🍺🍺",                 // mention then verb then emoji cluster w/ quantity
		"<@U12345> gives beer",                      // mention then verb then keyword
	}
	for _, c := range cases {
		if !bot.isBeerGiving(c) {
			// Fail fast with example of missed pattern
			// (pattern set should catch all above)
			// Keep output short for clarity.
			t.Fatalf("expected beer gift intent detected for: %q", c)
		}
	}
}

// TestNegativeBeerPatterns ensures non-gift messages do not match.
func TestNegativeBeerPatterns(t *testing.T) {
	bot := &MinimalSlackBot{}
	negatives := []string{
		"I love rootbeer <@U12345>",           // substring 'beer' not standalone giving intent
		"<@U12345> beermat is here",           // 'beer' part of longer word
		"We have a beer meetup",               // no mention-first giving intent
		"<@U12345> gearbox upgrade",           // 'gearbox' similar letters
		"Random text <@U12345> cheers",         // no beer tokens
		"Give everyone applause <@U12345>",    // give verb but no beer content
	}
	for _, c := range negatives {
		if bot.isBeerGiving(c) {
			// It's okay to log which pattern misfired if needed later; for now just fail.
			t.Fatalf("unexpected beer gift detection for: %q", c)
		}
	}
}
