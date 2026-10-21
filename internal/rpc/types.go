package rpc

type idReq struct {
	ID string `json:"id"`
}

type updateReq struct {
	ID    string    `json:"id"`
	Patch NodePatch `json:"patch"`
}

type checkPortReq struct {
	Port    int    `json:"port"`
	Exclude string `json:"exclude"`
}

type credReq struct {
	Cred string `json:"cred"`
}

type tailReq struct {
	N int `json:"n"`
}

type nodeResp struct {
	Node Node `json:"node"`
	OK   bool `json:"ok"`
}
