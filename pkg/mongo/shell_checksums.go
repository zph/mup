package mongo

import "fmt"

// shellArchiveSHA256 pins the SHA-256 of every mongosh release archive mup
// can download. The shell versions are pinned in shellCompatibilityByWireVersion,
// so their archives are fixed too.
//
// 2.5.9 values are the digests GitHub publishes for the release assets.
// 2.5.1 predates published digests; its values were computed from the release
// assets on 2026-11-20 and guard against the archives changing afterwards.
// LOCAL-034
var shellArchiveSHA256 = map[string]string{
	"mongosh-2.5.9-darwin-arm64.zip": "c82af6ec00ca2ef93f13be2c9edb00c23851ce4932ba20411ed028e35e365072",
	"mongosh-2.5.9-darwin-x64.zip":   "4205443008227e8a6ce1286419047bb281b407133d9762529a25f40d23987485",
	"mongosh-2.5.9-linux-arm64.tgz":  "01d6cb109e59769edfa5b44c93cc69bced8f39c216c3d4d1df471ecbc3f2d356",
	"mongosh-2.5.9-linux-x64.tgz":    "0644b6ac0b99dd52f46bdc4085aae19bb6d51226ef346c51418321769e76363e",

	"mongosh-2.5.1-darwin-arm64.zip": "05b810b2aaa0a8c2e743cc0853b66979d1354a9e8fc4c1be74333cc94ffc36b4",
	"mongosh-2.5.1-darwin-x64.zip":   "086b72c9a6be0e9e53bc16821d321147cddf205f4b41aaf34121443882b4663d",
	"mongosh-2.5.1-linux-arm64.tgz":  "174a46d2a9e64b5f3c67dfbabe4ada34350effeb4f6e8544a5f93f2f423a02d1",
	"mongosh-2.5.1-linux-x64.tgz":    "d2e252646bc99903fc3bc38ca816b05f27551600a834b37e3804b6ea8014fee6",
}

// Operating systems and architectures mongosh archives are pinned for, in
// the names mongosh uses for its release assets.
var (
	shellArchiveOSes  = map[string]string{"darwin": "zip", "linux": "tgz"}
	shellArchiveArchs = []string{"arm64", "x64"}
)

// ShellArchiveName returns the release asset name of a mongosh archive.
func ShellArchiveName(shellVersion, targetOS, arch, ext string) string {
	return fmt.Sprintf("mongosh-%s-%s-%s.%s", shellVersion, targetOS, arch, ext)
}

// ShellArchiveSHA256 returns the pinned SHA-256 of a mongosh release archive.
// LOCAL-034
func ShellArchiveSHA256(archiveName string) (string, bool) {
	sum, ok := shellArchiveSHA256[archiveName]
	return sum, ok
}
