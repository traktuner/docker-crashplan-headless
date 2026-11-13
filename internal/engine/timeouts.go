package engine

import "time"

const LoginAttempts = 4
const AccountRequestTimeout = 190 * time.Second

// A registration lookup can precede all four native-requested login attempts.
const AccountOperationTimeout = (LoginAttempts+1)*AccountRequestTimeout + 10*time.Second
