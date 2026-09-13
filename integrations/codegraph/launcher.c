// Set no_new_privs after the runtime has entered its AppArmor profile. Docker
// Snap cannot transition profiles when its --no-new-privileges flag is set before
// exec. This trusted launcher applies the same kernel restriction before Node.
#include <sys/prctl.h>
#include <unistd.h>
int main(void) {
  if (prctl(PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0) != 0) return 126;
  char *const args[] = {"/opt/codegraph/node", "/opt/conductor/extract.cjs", 0};
  execv(args[0], args);
  return 127;
}
