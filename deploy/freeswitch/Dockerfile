# FreeSWITCH with G.729 transcoding (D-86).
#
# The stock image ships FreeSWITCH's own mod_g729, which is a pass-through
# stub: it negotiates and relays G.729 but refuses to encode or decode it
# ("This codec is only usable in passthrough mode!"). This image replaces it
# with mod_bcg729, a real codec backed by Belledonne's bcg729, so a call that
# has to be transcoded into or out of G.729 works instead of failing.
#
# Everything the build needs is vendored under deploy/freeswitch, so the build
# does not depend on GitHub being up: src/mod_bcg729.c (the module),
# src/bcg729 (the codec library, pinned at 1.1.1) and include (the FreeSWITCH
# 1.10.12 build headers). See README.md for how to refresh them.
#
# The builder is debian:12 because the runtime image is Debian 12 (glibc 2.36).
# Building on a newer distro produces a module that needs a newer glibc and
# will not load.

FROM debian:12 AS build

RUN apt-get -qq update \
 && DEBIAN_FRONTEND=noninteractive apt-get -qq install -y --no-install-recommends \
      build-essential cmake \
 && rm -rf /var/lib/apt/lists/*

WORKDIR /build
COPY src/bcg729 ./bcg729
COPY src/mod_bcg729.c ./
COPY include ./include

# bcg729 as a static, position independent library, so the module has no extra
# runtime dependency to ship.
RUN cmake -S bcg729 -B bcg729-build \
      -DBUILD_SHARED_LIBS=OFF -DENABLE_UNIT_TESTS=NO \
      -DCMAKE_POSITION_INDEPENDENT_CODE=YES -DCMAKE_BUILD_TYPE=Release \
 && cmake --build bcg729-build -j"$(nproc)"

# The module itself. FreeSWITCH modules are dlopened into a process that
# already exports the switch_* symbols, so libfreeswitch is not linked here.
RUN gcc -shared -fPIC -O2 -Wall -Wextra -o mod_bcg729.so mod_bcg729.c \
      -Iinclude/freeswitch -Iinclude/apr -Iinclude/teletone -Ibcg729/include \
      bcg729-build/src/libbcg729.a -lm \
 && test -f mod_bcg729.so

FROM safarov/freeswitch:1.10.12

# The vendored headers are 1.10.12's. If the base image moves, the headers have
# to move with it: fail the build here rather than load a module built against
# the wrong ABI.
RUN freeswitch -version 2>&1 | grep -q "1\.10\.12" \
 || (echo "base image is no longer FreeSWITCH 1.10.12: re-vendor deploy/freeswitch/include" >&2; exit 1)

# Out with the pass-through stub, in with the codec. Removing the file is what
# makes this stick: nothing can load mod_g729 by accident afterwards.
RUN rm -f /usr/lib/freeswitch/mod/mod_g729.so
COPY --from=build /build/mod_bcg729.so /usr/lib/freeswitch/mod/mod_bcg729.so

RUN test -f /usr/lib/freeswitch/mod/mod_bcg729.so \
 && test ! -f /usr/lib/freeswitch/mod/mod_g729.so
