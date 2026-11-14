package app

// Optional features the owner hasn't decided on yet. Each one is behind a single
// switch here: set it to false and the server stops doing it and the page hides it.
// To remove one for good, search the code for its tag ("OPTIONAL:lamp" etc.) and
// delete the marked blocks; nothing else depends on them.
//
//	OPTIONAL:lamp       light as a status lamp (green healthy / red problem)
//	OPTIONAL:bestflash  short light flash on a new personal-best share
//	OPTIONAL:badges     badges and streaks
//	OPTIONAL:sparkline  Mac menu bar hashrate sparkline
//
// The block-found gold flash is a kept feature, not optional.
var Optional = OptionalFeatures{StatusLamp: true, BestFlash: true, Badges: true, MenuSparkline: true}

type OptionalFeatures struct {
	StatusLamp    bool `json:"status_lamp"`
	BestFlash     bool `json:"best_flash"`
	Badges        bool `json:"badges"`
	MenuSparkline bool `json:"menu_sparkline"`
}
