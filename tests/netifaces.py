"""Minimal netifaces stand-in for the NEX test client.

anynet imports netifaces only to discover the local interface address and
netmask, which an outbound client connecting to 127.0.0.1 never needs. The real
package has no Windows wheel for this Python version and needs a C toolchain to
build, so this supplies just the three names anynet touches.
"""

import socket

AF_INET = socket.AF_INET
AF_INET6 = socket.AF_INET6
AF_LINK = 18

_INTERFACE = "lo0"


def _local_address():
    try:
        probe = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        try:
            probe.connect(("8.8.8.8", 80))
            return probe.getsockname()[0]
        finally:
            probe.close()
    except OSError:
        return "127.0.0.1"


def interfaces():
    return [_INTERFACE]


def gateways():
    return {"default": {AF_INET: ("0.0.0.0", _INTERFACE)}}


def ifaddresses(interface):
    address = _local_address()
    return {
        AF_INET: [
            {
                "addr": address,
                "netmask": "255.255.255.0",
                "broadcast": "255.255.255.255",
            }
        ]
    }
