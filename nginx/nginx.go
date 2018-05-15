package nginx

type Processes struct {
	Respawned *int `json:"respawned"`
}

type Connections struct {
	Accepted int `json:"accepted"`
	Dropped  int `json:"dropped"`
	Active   int `json:"active"`
	Idle     int `json:"idle"`
}

type Ssl struct {
	Handshakes       int64 `json:"handshakes"`
	HandshakesFailed int64 `json:"handshakes_failed"`
	SessionReuses    int64 `json:"session_reuses"`
}

type Requests struct {
	Total   int64 `json:"total"`
	Current int   `json:"current"`
}
type HttpServerZones map[string]struct {
	Processing int   `json:"processing"`
	Requests   int64 `json:"requests"`
	Responses  struct {
		Responses1xx int64 `json:"1xx"`
		Responses2xx int64 `json:"2xx"`
		Responses3xx int64 `json:"3xx"`
		Responses4xx int64 `json:"4xx"`
		Responses5xx int64 `json:"5xx"`
		Total        int64 `json:"total"`
	} `json:"responses"`
	Discarded int64 `json:"discarded"`
	Received  int64 `json:"received"`
	Sent      int64 `json:"sent"`
}

type HttpUpstreams map[string]struct {
	Peers []struct {
		Server   string `json:"server"`
		Service  string `json:"service"`
		Name     string `json:"name"`
		Backup   bool   `json:"backup"`
		Weight   int    `json:"weight"`
		State    string `json:"state"`
		Active   int    `json:"active"`
		MaxConns int    `json:"max_conns"`
		Requests int64  `json:"requests"`
		Responses struct {
			Responses1xx int64 `json:"1xx"`
			Responses2xx int64 `json:"2xx"`
			Responses3xx int64 `json:"3xx"`
			Responses4xx int64 `json:"4xx"`
			Responses5xx int64 `json:"5xx"`
			Total        int64 `json:"total"`
		} `json:"responses"`
		Sent     int64 `json:"sent"`
		Received int64 `json:"received"`
		Fails    int64 `json:"fails"`
		Unavail  int64 `json:"unavail"`
		HealthChecks struct {
			Checks     int64 `json:"checks"`
			Fails      int64 `json:"fails"`
			Unhealthy  int64 `json:"unhealthy"`
			LastPassed bool  `json:"last_passed"`
		} `json:"health_checks"`
		Downtime     int64 `json:"downtime"`
		HeaderTime   int64 `json:"header_time"`
		ResponseTime int64 `json:"response_time"`
	} `json:"peers"`
	Keepalive int `json:"keepalive"`
	Zombies   int `json:"zombies"`
	Queue *struct {
		Size      int   `json:"size"`
		MaxSize   int   `json:"max_size"`
		Overflows int64 `json:"overflows"`
	} `json:"queue"`
}

type Caches map[string]struct {
	Size    int64 `json:"size"`
	MaxSize int64 `json:"max_size"`
	Cold    bool  `json:"cold"`
	Hit     struct {
		Responses int64 `json:"responses"`
		Bytes     int64 `json:"bytes"`
	} `json:"hit"`
	Stale struct {
		Responses int64 `json:"responses"`
		Bytes     int64 `json:"bytes"`
	} `json:"stale"`
	Updating struct {
		Responses int64 `json:"responses"`
		Bytes     int64 `json:"bytes"`
	} `json:"updating"`
	Revalidated *struct {
		Responses int64 `json:"responses"`
		Bytes     int64 `json:"bytes"`
	} `json:"revalidated"`
	Miss struct {
		Responses        int64 `json:"responses"`
		Bytes            int64 `json:"bytes"`
		ResponsesWritten int64 `json:"responses_written"`
		BytesWritten     int64 `json:"bytes_written"`
	} `json:"miss"`
	Expired struct {
		Responses        int64 `json:"responses"`
		Bytes            int64 `json:"bytes"`
		ResponsesWritten int64 `json:"responses_written"`
		BytesWritten     int64 `json:"bytes_written"`
	} `json:"expired"`
	Bypass struct {
		Responses        int64 `json:"responses"`
		Bytes            int64 `json:"bytes"`
		ResponsesWritten int64 `json:"responses_written"`
		BytesWritten     int64 `json:"bytes_written"`
	} `json:"bypass"`
}

type StreamServerZones map[string]struct {
	Processing  int `json:"processing"`
	Connections int `json:"connections"`
	Sessions    *struct {
		Total       int64 `json:"total"`
		Sessions1xx int64 `json:"1xx"`
		Sessions2xx int64 `json:"2xx"`
		Sessions3xx int64 `json:"3xx"`
		Sessions4xx int64 `json:"4xx"`
		Sessions5xx int64 `json:"5xx"`
	} `json:"sessions"`
	Discarded int64 `json:"discarded"`
	Received  int64 `json:"received"`
	Sent      int64 `json:"sent"`
}

type StreamUpstreams map[string]struct {
	Peers []struct {
		ID            int    `json:"id"`
		Server        string `json:"server"`
		Backup        bool   `json:"backup"`
		Weight        int    `json:"weight"`
		State         string `json:"state"`
		Active        int    `json:"active"`
		Connections   int64  `json:"connections"`
		ConnectTime   int    `json:"connect_time"`
		FirstByteTime int    `json:"first_byte_time"`
		ResponseTime  int    `json:"response_time"`
		Sent          int64  `json:"sent"`
		Received      int64  `json:"received"`
		Fails         int64  `json:"fails"`
		Unavail       int64  `json:"unavail"`
		MaxConns      int    `json:"max_conns"`
		HealthChecks  struct {
			Checks     int64 `json:"checks"`
			Fails      int64 `json:"fails"`
			Unhealthy  int64 `json:"unhealthy"`
			LastPassed bool  `json:"last_passed"`
		} `json:"health_checks"`
		Downtime  int64 `json:"downtime"`
	} `json:"peers"`
	Zombies int `json:"zombies"`
}
