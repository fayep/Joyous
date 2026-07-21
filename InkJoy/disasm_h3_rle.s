4201c766:  c.addi   sp, -0x20
4201c768:  c.swsp   ra, 0x1c(sp)
4201c76a:  c.swsp   s0, 0x18(sp)
4201c76c:  c.swsp   s1, 0x14(sp)
4201c76e:  c.swsp   s2, 0x10(sp)
4201c770:  c.swsp   s3, 0xc(sp)
4201c772:  c.swsp   s4, 8(sp)
4201c774:  c.swsp   s5, 4(sp)
4201c776:  c.mv     s3, a0
4201c778:  lui      a0, 0x40829
4201c77c:  addi     s1, a0, -0x37c  ; mem 0x40828c84
4201c780:  c.li     s0, 0xd
4201c782:  c.mv     a2, s0
4201c784:  c.mv     a1, s3
4201c786:  addi     a0, a0, -0x37c  ; mem 0x40828c84
4201c78a:  auipc    ra, 0xfdfe4
4201c78e:  jalr     ra, ra, -0x2ce
4201c792:  c.mv     a2, s0
4201c794:  c.mv     a1, s3
4201c796:  lui      a0, 0x40829
4201c79a:  addi     a0, a0, 0xb8  ; mem 0x408290b8
4201c79e:  auipc    ra, 0xfdfe4
4201c7a2:  jalr     ra, ra, -0x2e2
4201c7a6:  lbu      a4, 0xc(s1)
4201c7aa:  addi     a5, zero, 0xff
4201c7ae:  beq      a4, a5, 0xbc  -> 4201c86a
4201c7b2:  lui      s0, 0x40829
4201c7b6:  addi     a5, s0, -0x37c  ; mem 0x40828c84
4201c7ba:  lbu      a2, 0xc(a5)
4201c7be:  c.slli   a2, 2
4201c7c0:  addi     a1, s3, 0xd
4201c7c4:  addi     a0, s0, -0x37c  ; mem 0x40828c84
4201c7c8:  auipc    ra, 0xfdfe4
4201c7cc:  jalr     ra, ra, -0x30c
4201c7d0:  lui      a0, 0x40829
4201c7d4:  addi     a2, zero, 0x3fc
4201c7d8:  addi     a1, s0, -0x37c  ; mem 0x40828c84
4201c7dc:  addi     a0, a0, 0xb8  ; mem 0x408290b8
4201c7e0:  c.addi   a0, 0xd
4201c7e2:  auipc    ra, 0xfdfe4
4201c7e6:  jalr     ra, ra, -0x326
4201c7ea:  c.li     a1, 0
4201c7ec:  lui      a0, 0x40829
4201c7f0:  addi     a0, a0, 0xc5  ; mem 0x408290c5
4201c7f4:  jal      -0x284
4201c7f8:  c.mv     s4, a0
4201c7fa:  lui      s1, 0x40829
4201c7fe:  addi     s0, s1, -0x398  ; mem 0x40828c68
4201c802:  c.li     a2, 0x15
4201c804:  add      a1, s3, a0
4201c808:  addi     a0, s1, -0x398  ; mem 0x40828c68
4201c80c:  auipc    ra, 0xfdfe4
4201c810:  jalr     ra, ra, -0x350
4201c814:  lbu      s2, 0x10(s0)
4201c818:  lui      s0, 0x40829
4201c81c:  addi     s0, s0, 0xa0  ; mem 0x408290a0
4201c820:  sb       s2, 0x10(s0)
4201c824:  c.li     a1, 0x11
4201c826:  addi     a0, s1, -0x398  ; mem 0x40828c68
4201c82a:  jal      -0x2ba
4201c82e:  c.sw     a0, 0x14(s0)
4201c830:  c.li     a1, 0
4201c832:  addi     a0, s1, -0x398  ; mem 0x40828c68
4201c836:  jal      -0x2c6
4201c83a:  c.sw     a0, 0(s0)
4201c83c:  c.li     a1, 4
4201c83e:  addi     a0, s1, -0x398  ; mem 0x40828c68
4201c842:  jal      -0x2d2
4201c846:  c.sw     a0, 4(s0)
4201c848:  c.li     a1, 8
4201c84a:  addi     a0, s1, -0x398  ; mem 0x40828c68
4201c84e:  jal      -0x2de
4201c852:  c.sw     a0, 8(s0)
4201c854:  c.li     a1, 0xc
4201c856:  addi     a0, s1, -0x398  ; mem 0x40828c68
4201c85a:  jal      -0x2ea
4201c85e:  c.sw     a0, 0xc(s0)
4201c860:  c.li     a5, 1
4201c862:  beq      s2, a5, 0x120  -> 4201c982
4201c866:  c.li     s1, 0
4201c868:  c.j      0x140  -> 4201c9a8
4201c86a:  lui      a5, 0x40829
4201c86e:  addi     a5, a5, -0x37c  ; mem 0x40828c84
4201c872:  sb       zero, 0xc(a5)
4201c876:  c.j      -0xc4  -> 4201c7b2
4201c878:  auipc    ra, 0xfe7fd
4201c87c:  jalr     ra, ra, -0x6bc
4201c880:  c.mv     a3, a0
4201c882:  c.mv     a5, s2
4201c884:  lui      a1, 0x42168
4201c888:  addi     a4, a1, -0x408  ; "EPD"
4201c88c:  lui      a2, 0x42168
4201c890:  addi     a2, a2, -0x384  ; "RLE err: zero-length run at pcnt=%d[0m
"
4201c894:  addi     a1, a1, -0x408  ; "EPD"
4201c898:  c.li     a0, 1
4201c89a:  auipc    ra, 0xfe7fd
4201c89e:  jalr     ra, ra, -0x7f0
4201c8a2:  c.j      0x96  -> 4201c938
4201c8a4:  auipc    ra, 0xfe7fd
4201c8a8:  jalr     ra, ra, -0x6e8
4201c8ac:  c.mv     a3, a0
4201c8ae:  lui      a6, 0xea
4201c8b2:  addi     a6, a6, 0x600  ; PIC_SIZE=960000 (1200*800)
4201c8b6:  c.mv     a5, s2
4201c8b8:  lui      a1, 0x42168
4201c8bc:  addi     a4, a1, -0x408  ; "EPD"
4201c8c0:  lui      a2, 0x42168
4201c8c4:  addi     a2, a2, -0x348  ; "RLE overflow: pcnt=%d > PIC_SIZE=%d[0m
"
4201c8c8:  addi     a1, a1, -0x408  ; "EPD"
4201c8cc:  c.li     a0, 1
4201c8ce:  auipc    ra, 0xfe7fc
4201c8d2:  jalr     ra, ra, 0x7dc
4201c8d6:  c.lui    a5, 1
4201c8d8:  c.add    s5, a5
4201c8da:  lui      a5, 0xea
4201c8de:  addi     a5, a5, 0x5ff  ; PIC_SIZE-1
4201c8e2:  blt      a5, s2, 0xe6  -> 4201c9c8
4201c8e6:  add      a1, s4, s5
4201c8ea:  c.lui    a2, 1
4201c8ec:  c.add    a1, s3
4201c8ee:  lui      a0, 0x40828
4201c8f2:  addi     a0, a0, -0x398  ; mem 0x40827c68
4201c8f6:  auipc    ra, 0xfdfe4
4201c8fa:  jalr     ra, ra, -0x43a
4201c8fe:  c.li     s0, 0
4201c900:  c.j      0x3a  -> 4201c93a
4201c902:  addi     a3, s0, 1  ; mem 0x40829001
4201c906:  lui      a5, 0x4082a
4201c90a:  lw       a4, 0x330(a5)
4201c90e:  c.add    a4, s1
4201c910:  lui      a5, 0x40828
4201c914:  addi     a5, a5, -0x398  ; mem 0x40827c68
4201c918:  c.add    a5, a3
4201c91a:  lbu      a5, 0(a5)
4201c91e:  sb       a5, 0(a4)
4201c922:  c.addi   s1, 1
4201c924:  c.addi   a2, 1
4201c926:  lui      a5, 0x40828
4201c92a:  addi     a5, a5, -0x398  ; mem 0x40827c68
4201c92e:  c.add    a5, s0
4201c930:  lbu      a5, 0(a5)
4201c934:  bltu     a2, a5, -0x32  -> 4201c902
4201c938:  c.addi   s0, 2
4201c93a:  c.lui    a5, 1
4201c93c:  bgeu     s0, a5, -0x66  -> 4201c8d6
4201c940:  lui      a5, 0xea
4201c944:  addi     a5, a5, 0x5ff  ; PIC_SIZE-1
4201c948:  blt      a5, s2, -0x10  -> 4201c938
4201c94c:  lui      a5, 0x40828
4201c950:  addi     a5, a5, -0x398  ; mem 0x40827c68
4201c954:  c.add    a5, s0
4201c956:  lbu      a3, 0(a5)
4201c95a:  c.bnez   a3, 0x16  -> 4201c970
4201c95c:  addi     a4, s0, 1  ; mem 0x40829001
4201c960:  lui      a5, 0x40828
4201c964:  addi     a5, a5, -0x398  ; mem 0x40827c68
4201c968:  c.add    a5, a4
4201c96a:  lbu      a5, 0(a5)
4201c96e:  c.beqz   a5, -0xf6  -> 4201c878
4201c970:  c.add    s2, a3
4201c972:  lui      a5, 0xea
4201c976:  addi     a5, a5, 0x600  ; PIC_SIZE=960000 (1200*800)
4201c97a:  blt      a5, s2, -0xd6  -> 4201c8a4
4201c97e:  c.li     a2, 0
4201c980:  c.j      -0x5a  -> 4201c926
4201c982:  c.li     s1, 0
4201c984:  c.li     s2, 0
4201c986:  c.li     s5, 0x15
4201c988:  c.j      -0xae  -> 4201c8da
4201c98a:  lui      a5, 0x4082a
4201c98e:  lw       a0, 0x330(a5)
4201c992:  addi     a1, s4, 0x15
4201c996:  c.add    a1, s1
4201c998:  c.mv     a2, s0
4201c99a:  c.add    a1, s3
4201c99c:  c.add    a0, s1
4201c99e:  auipc    ra, 0xfdfe4
4201c9a2:  jalr     ra, ra, -0x4e2
4201c9a6:  c.add    s1, s0
4201c9a8:  lui      a5, 0xea
4201c9ac:  addi     a5, a5, 0x5ff  ; PIC_SIZE-1
4201c9b0:  blt      a5, s1, 0x18  -> 4201c9c8
4201c9b4:  lui      s0, 0xea
4201c9b8:  addi     s0, s0, 0x600  ; PIC_SIZE=960000 (1200*800)
4201c9bc:  c.sub    s0, s1
4201c9be:  c.lui    a5, 1
4201c9c0:  blt      s0, a5, -0x36  -> 4201c98a
4201c9c4:  c.mv     s0, a5
4201c9c6:  c.j      -0x3c  -> 4201c98a
4201c9c8:  c.mv     a0, s3
4201c9ca:  jal      -0x2f6
4201c9ce:  c.lwsp   ra, 0x1c(sp)
4201c9d0:  c.lwsp   s0, 0x18(sp)
4201c9d2:  c.lwsp   s1, 0x14(sp)
4201c9d4:  c.lwsp   s2, 0x10(sp)
4201c9d6:  c.lwsp   s3, 0xc(sp)
4201c9d8:  c.lwsp   s4, 8(sp)
4201c9da:  c.lwsp   s5, 4(sp)
4201c9dc:  c.addi16sp sp, 0x20
4201c9de:  c.jr     ra
