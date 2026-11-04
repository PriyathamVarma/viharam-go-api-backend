package experiences

type Experience struct {
	ID                  string `json:"id"`
	Slug                string `json:"slug"`
	Title               string `json:"title"`
	ShortDescription    string `json:"shortDescription,omitempty"`
	Description         string `json:"description"`
	Category            string `json:"category"`
	City                string `json:"city"`
	State               string `json:"state"`
	Village             string `json:"village,omitempty"`
	PricePerPersonPaise int64  `json:"pricePerPersonPaise"`
	DurationMinutes     int    `json:"durationMinutes"`
	MinGroupSize        int    `json:"minGroupSize"`
	MaxGroupSize        int    `json:"maxGroupSize"`
	Status              string `json:"status"`
	IsFeatured          bool   `json:"isFeatured"`
	HostID              string `json:"hostId"`
	CreatedAt           string `json:"createdAt"`
	UpdatedAt           string `json:"updatedAt"`
}

type SearchQuery struct {
	Q        string
	Category string
	City     string
	State    string
	Featured bool
	Page     int
	Limit    int
}
