release {
  provider                = "github"
  url                     = "https://github.com/openkcm/platform-mesh-controller/releases"
  changelog               = "CHANGELOG.md"
  tag_prefix              = "v"
  release_name_template   = "Release {{.Version}}"
  assets = [
    "*"
  ]
}
