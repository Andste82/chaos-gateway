"""The script mitmproxy loads (`mitmdump -s entry.py`): it makes the addon from the environment."""

import os
import sys

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))

from chaosgw_tlsproxy.addon import from_environment  # noqa: E402

addons = [from_environment()]
