#!/bin/sh

set -e
image="olareg"
platforms="linux/386,linux/amd64,linux/arm/v6,linux/arm/v7,linux/arm64,linux/ppc64le,linux/s390x"
base_name=""
release="scratch"
push_tags=""

# CLI options to override image, platform, base digest, and comma separated list of tags to push
opt_c=0
opt_h=0
while getopts 'b:cd:hi:p:r:t:' option; do
  case $option in
    b) base_name="$OPTARG";;
    c) opt_c=1;;
    d) base_digest="$OPTARG";;
    h) opt_h=1;;
    i) image="$OPTARG";;
    p) platforms="$OPTARG";;
    r) release="$OPTARG";;
    t) push_tags="$OPTARG";;
  esac
done
set +e
shift $(expr $OPTIND - 1)
if [ $# -gt 0 -o "$opt_h" = "1" ]; then
  echo "Usage: $0 [opts]"
  echo " -b: base image name"
  echo " -c: use cache"
  echo " -d: base image digest"
  echo " -h: this help message"
  echo " -i: image to build (${image})"
  echo " -p: platforms to build (${platforms})"
  echo " -r: release target (${release})"
  echo " -t: tags to push (comma separated image list)"
  exit 1
fi
set -e

# cd to repo root, gather details from git, and build images
git_root="$(git rev-parse --show-toplevel)"
cd "${git_root}"
export PATH="$PATH:${git_root}/bin"
now_date="$(date +%Y-%m-%dT%H:%M:%SZ --utc)"
vcs_sec="$(git log -1 --format=%ct)"
vcs_date="$(date -d "@${vcs_sec}" +%Y-%m-%dT%H:%M:%SZ --utc)"
vcs_repo="https://github.com/regclient/regclient.git"
vcs_sha="$(git rev-list -1 HEAD)"
vcs_describe="$(git describe --all)"
vcs_version="noop"
if [ "${vcs_describe}" != "${vcs_describe#tags/}" ]; then
  vcs_version="${vcs_describe#tags/}"
elif [ "${vcs_describe}" != "${vcs_describe#heads/}" ]; then
  vcs_version="${vcs_describe#heads/}"
  if [ "main" = "$vcs_version" ]; then
    vcs_version=edge
  fi
fi
vcs_version="$(echo "${vcs_version}" | sed -r 's#/+#-#g')"

build_opts=""
if [ -n "$base_name" ] && [ -z "$base_digest" ]; then
  base_digest="$(regctl image digest "${base_name}")"
  echo "Base image digest: ${base_digest}"
elif [ -n "$base_name" ] && [ -n "$base_digest" ]; then
  build_opts=--build-context "${base_name}=docker-image://${base_name}@${base_digest}"
fi
[ -d "output" ] || mkdir -p output
if [ "${opt_c}" = "0" ]; then
  build_opts="$build_opts --no-cache"
fi
docker buildx build --platform="$platforms" \
  -f "build/Dockerfile.${image}.buildkit" \
  -o "type=oci,dest=output/${image}-${release}.tar" \
  --target "release-${release}" \
  --build-arg "SOURCE_DATE_EPOCH=${vcs_sec}" \
  --label org.opencontainers.image.created=${vcs_date} \
  --label org.opencontainers.image.source=${vcs_repo} \
  --label org.opencontainers.image.version=${vcs_version} \
  --label org.opencontainers.image.revision=${vcs_sha} \
  ${build_opts} .

echo "Importing tar"
regctl tag rm "ocidir://output/${image}:${release}" 2>/dev/null || true
regctl image import "ocidir://output/${image}:${release}" "output/${image}-${release}.tar"
echo "Modding image"
regctl image mod \
  "ocidir://output/${image}:${release}" --replace \
  --to-oci-referrers
regctl image mod \
  "ocidir://output/${image}:${release}" --replace \
  --annotation "[*]org.opencontainers.image.created=${vcs_date}" \
  --annotation "[*]org.opencontainers.image.source=${vcs_repo}" \
  --annotation "[*]org.opencontainers.image.version=${vcs_version}" \
  --annotation "[*]org.opencontainers.image.revision=${vcs_sha}" \
  >/dev/null

if [ -n "$base_name" ] && [ -n "$base_digest" ]; then
  regctl image mod \
    "ocidir://output/${image}:${release}" --replace \
    --annotation "[*]org.opencontainers.image.base.name=${base_name}" \
    --annotation "[*]org.opencontainers.image.base.digest=${base_digest}" \
    --reproducible \
    --time "set=${vcs_date},base-ref=${base_name}@${base_digest}" \
    >/dev/null
else
  regctl image mod \
    "ocidir://output/${image}:${release}" --replace \
    --reproducible \
    --time "set=${vcs_date}" \
    >/dev/null
fi

# attach sboms to each platform
for digest in $(regctl manifest get ocidir://output/${image}:${release} --format '{{range .Manifests}}{{printf "%s\n" .Digest}}{{end}}'); do
  echo "Attaching SBOMs for ${image}:${release}@${digest}"
  regctl image copy ocidir://output/${image}@${digest} ocidir://output/${image}-sbom -v warn >/dev/null
  syft packages -q "oci-dir:output/${image}-sbom" --source-name "docker:docker.io/regclient/${image}@${digest}" -o cyclonedx-json \
    | regctl artifact put --subject "ocidir://output/${image}@${digest}" \
        --artifact-type application/vnd.cyclonedx+json \
        -m application/vnd.cyclonedx+json \
        --annotation "org.opencontainers.image.created=${now_date}" \
        --annotation "org.opencontainers.image.description=CycloneDX JSON SBOM"
  syft packages -q "oci-dir:output/${image}-sbom" --source-name "docker:docker.io/regclient/${image}@${digest}" -o spdx-json \
    | regctl artifact put --subject "ocidir://output/${image}@${digest}" \
        --artifact-type application/spdx+json \
        -m application/spdx+json \
        --annotation "org.opencontainers.image.created=${now_date}" \
        --annotation "org.opencontainers.image.description=SPDX JSON SBOM"
  rm -r output/${image}-sbom
done

# manually prune old digest tags from previous builds and before the mod
for tag in $(regctl tag ls ocidir://output/${image}); do
  if [ "${tag}" != "${tag#sha256-}" ] && ! regctl manifest head "ocidir://output/${image}@sha256:${tag#sha256-}" 2>/dev/null; then
    regctl tag rm "ocidir://output/${image}:${tag}"
  fi
done

echo "\033[32mDigest for ${image}-${release}:\033[0m $(regctl image digest "ocidir://output/${image}:${release}")"

# split tags by comma and push each tag
if [ -n "$push_tags" ]; then
  for push_tag in $(echo "$push_tags" | tr , " "); do
    echo "Push: ${push_tag}"
    regctl image copy -v info "ocidir://output/${image}:${release}" "${push_tag}"
  done
fi