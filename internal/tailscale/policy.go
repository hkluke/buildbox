package tailscale

import (
	"strings"

	"github.com/hkluke/buildbox/internal/config"
)

// Admin console pages that init and create point at. The console moved to
// console.tailscale.com in 2026; the old login.tailscale.com/admin paths
// redirect there.
const (
	PolicyURL = "https://console.tailscale.com/admin/acls"
	KeysURL   = "https://console.tailscale.com/admin/settings/keys"
	OAuthURL  = "https://console.tailscale.com/admin/settings/oauth"
)

// PolicyBlock is one addition to the tailnet policy: the section it belongs
// in and the line to paste there.
type PolicyBlock struct {
	Section string
	Line    string
}

// PolicyBlocks are the additions boxes carrying tag need. The tag's owner
// is the tailnet owner, so only they mint keys for it. Boxes are never a
// source, so a box reaches nothing on the tailnet, while the owner's devices
// reach boxes on every port. The ssh rule decides who logs in, as which user,
// since Tailscale SSH answers port 22 on the tailnet instead of OpenSSH.
func PolicyBlocks(tag string) []PolicyBlock {
	return []PolicyBlock{
		{sectionTagOwners, tagOwnerLine(tag)},
		{sectionACLs, aclLine(tag)},
		{sectionSSH, SSHRule(tag)},
	}
}

func tagOwnerLine(tag string) string {
	return `"` + tag + `": ["` + ownerGroup + `"],`
}

func aclLine(tag string) string {
	return `{"action": "accept", "src": ["` + ownerGroup + `"], "dst": ["` + tag + `:*"]},`
}

// grantLine is the acls line for a policy written with the grants syntax.
func grantLine(tag string) string {
	return `{"src": ["` + ownerGroup + `"], "dst": ["` + tag + `"], "ip": ["*"]},`
}

// SSHRule is the ssh block alone, which create shows when the policy refuses
// the first login to a new box.
func SSHRule(tag string) string {
	return sshRule(tag, boxUsers())
}

// boxUsers are the logins every box has, in the order the rule lists them.
func boxUsers() []string {
	return []string{config.AgentUser, config.AdminUser}
}

func sshRule(tag string, users []string) string {
	quoted := make([]string, 0, len(users))
	for _, user := range users {
		quoted = append(quoted, `"`+user+`"`)
	}

	return `{"action": "accept", "src": ["` + ownerGroup + `"], "dst": ["` + tag + `"], ` +
		`"users": [` + strings.Join(quoted, ", ") + `]},`
}
