# SSH per-user traffic and speed limits

This build adds per-user Total Flow, usage reset cycles, manual traffic reset,
and separate download/upload speed limits to SSH Manager.

Implementation notes:
- Traffic accounting uses iptables mangle counters keyed by Linux UID and
  conntrack marks. The panel samples counters every 15 seconds.
- Reaching Total Flow automatically locks the Linux SSH account.
- Download shaping uses tc/HTB on the default network interface.
- Upload limiting uses tc ingress policing with restored conntrack marks.
- The host needs `iptables`, `ip`, and `tc` (iproute2), which are normally
  present on supported Debian/Ubuntu systems.
- Existing custom root qdisc configurations may prevent tc classes from being
  installed; the panel logs and continues without interrupting startup.
