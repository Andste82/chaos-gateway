"""Tests of the batches the proxy reports to the gateway (no mitmproxy needed):

    python3 -m unittest discover -s tests -t .      (from sidecars/tls-proxy)
"""

import threading
import unittest

from chaosgw_tlsproxy.api import ApiError, Batcher, is_rejection


class Poster:
    """Stands in for the API: answers with the errors it is given, in order, then takes everything."""

    def __init__(self, *errors):
        self.errors = list(errors)
        self.batches = []
        self.calls = 0
        self.taken = threading.Event()

    def __call__(self, batch):
        self.calls += 1
        if self.errors:
            raise self.errors.pop(0)
        self.batches.append(list(batch))
        self.taken.set()


class BatcherTests(unittest.TestCase):
    def run_batcher(self, poster, *items, batch=100):
        b = Batcher("test", poster, batch=batch, retry=0.01)
        b.start()
        for i in items:
            b.add(i)
        return b

    def test_what_the_api_cannot_take_now_is_sent_again(self):
        for err in (OSError("the API is away"), ApiError(503, "busy"), ApiError(500, "failed"), ApiError(429, "slow down")):
            poster = Poster(err)
            b = self.run_batcher(poster, {"n": 1})
            self.assertTrue(poster.taken.wait(5), err)
            b.close()
            self.assertEqual(poster.batches, [[{"n": 1}]], err)
            self.assertEqual(b.refused, 0)

    def test_a_batch_the_api_refuses_for_its_content_is_dropped_and_does_not_block_the_next(self):
        poster = Poster(ApiError(422, "exchange 0: original_dst is not an address"))
        b = self.run_batcher(poster, {"n": "bad"})
        for _ in range(500):
            if b.refused:
                break
            threading.Event().wait(0.01)
        self.assertEqual(b.refused, 1)
        b.add({"n": "good"})
        self.assertTrue(poster.taken.wait(5))
        b.close()
        self.assertEqual(poster.batches, [[{"n": "good"}]])
        self.assertEqual(poster.calls, 2, "the refused batch was sent once")

    def test_a_batch_that_is_too_big_goes_again_in_smaller_ones(self):
        poster = Poster(ApiError(413, "the request body exceeds the limit"))
        b = Batcher("test", poster, batch=4, retry=0.01)
        for n in range(4):
            b.add({"n": n})
        b.start()
        for _ in range(500):
            if sum(len(x) for x in poster.batches) == 4:
                break
            threading.Event().wait(0.01)
        b.close()
        self.assertEqual([i["n"] for x in poster.batches for i in x], [0, 1, 2, 3], "nothing is lost or reordered")
        self.assertTrue(all(len(x) <= 2 for x in poster.batches), poster.batches)
        self.assertEqual(b.refused, 0)

    def test_which_errors_are_refusals(self):
        self.assertTrue(is_rejection(ApiError(400, "")))
        self.assertTrue(is_rejection(ApiError(422, "")))
        self.assertTrue(is_rejection(ApiError(413, "")))
        for e in (ApiError(408, ""), ApiError(429, ""), ApiError(500, ""), ApiError(503, ""), OSError("x"), ValueError("x")):
            self.assertFalse(is_rejection(e), e)

    def test_past_the_limit_the_oldest_are_dropped(self):
        poster = Poster()
        b = Batcher("test", poster, batch=2, limit=3)  # not started: nothing is sent
        for n in range(5):
            b.add({"n": n})
        self.assertEqual([i["n"] for i in b._items], [2, 3, 4])
        self.assertEqual(b.dropped, 2)


if __name__ == "__main__":
    unittest.main()
