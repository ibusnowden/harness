package modes

import "strconv"

type Report struct {
	Mode      string
	Connected bool
	Detail    string
	Target    string
	Active    bool
}

func (r Report) Text() string {
	if r.Target != "" {
		return "mode=" + r.Mode + "\ntarget=" + r.Target + "\nactive=" + strconv.FormatBool(r.Active)
	}
	return "mode=" + r.Mode + "\nconnected=" + strconv.FormatBool(r.Connected) + "\ndetail=" + r.Detail
}

func Remote(target string) Report {
	return Report{Mode: "remote", Connected: true, Detail: "Remote control placeholder prepared for " + target}
}

func SSH(target string) Report {
	return Report{Mode: "ssh", Connected: true, Detail: "SSH proxy placeholder prepared for " + target}
}

func Teleport(target string) Report {
	return Report{Mode: "teleport", Connected: true, Detail: "Teleport resume/create placeholder prepared for " + target}
}

func DirectConnect(target string) Report {
	return Report{Mode: "direct-connect", Target: target, Active: true}
}

func DeepLink(target string) Report {
	return Report{Mode: "deep-link", Target: target, Active: true}
}
