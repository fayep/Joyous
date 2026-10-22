from xtroadmap.xtensa import calln_target


def test_call8_target_matches_known_wifi_site():
    # 0x4037555b: e5 42 01  -> call8 to 0x40376988 (encoding, not Capstone text)
    assert calln_target(0x4037555B, bytes.fromhex("e54201")) == 0x40376988
