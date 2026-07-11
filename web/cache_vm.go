package web

// sevClass maps a T1 severity token to its cache CSS colour class.
func sevClass(sev string) string {
	switch sev {
	case "crit":
		return "c-sev-crit"
	case "warn":
		return "c-sev-warn"
	case "info":
		return "c-sev-info"
	case "ok":
		return "c-sev-ok"
	default:
		return "c-sev-mut"
	}
}

// roleClass maps a cache node role to its chip classes.
func roleClass(role string) string {
	if role == "PRIMARY" {
		return "c-role c-role-primary"
	}
	return "c-role c-role-replica"
}

// accessClass maps a cache node access mode to its badge classes.
// PRIMARY nodes accept writes (READ-WRITE); replicas are READ-ONLY.
func accessClass(access string) string {
	if access == "READ-WRITE" {
		return "c-access c-access-rw"
	}
	return "c-access c-access-ro"
}

// nextSortKey returns the sort key to toggle to, given the current key and the
// two options a/b. Used to build the SORT button's hx-get target.
func nextSortKey(cur, a, b string) string {
	if cur == a {
		return b
	}
	return a
}
