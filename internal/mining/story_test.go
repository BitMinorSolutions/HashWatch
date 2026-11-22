package mining

import "testing"

func TestStoryLine(t *testing.T) {
	got := StoryLine(5.1, 977100, true)
	want := "Your miner is guessing about 5.1 trillion numbers a second, trying to find a winning one for Block 977,100."
	if got != want {
		t.Fatalf("got %q", got)
	}
	if got := StoryLine(5.1, 0, false); got != "Your miner is guessing about 5.1 trillion numbers a second, trying to find a winning one." {
		t.Fatal(got)
	}
	if got := StoryLine(0, 977100, true); got != "Your miner is trying to find a winning one for Block 977,100." {
		t.Fatal(got)
	}
	if got := StoryLine(0, 0, false); got != "Your miner is trying to find a winning Bitcoin block." {
		t.Fatal(got)
	}
}

func TestWordsAndComma(t *testing.T) {
	if Words(5.1e12) != "5.1 trillion" || Words(1.3e6) != "1.3 million" {
		t.Fatalf("%q %q", Words(5.1e12), Words(1.3e6))
	}
	if Comma(977100) != "977,100" || Comma(21) != "21" {
		t.Fatal(Comma(977100), Comma(21))
	}
}
