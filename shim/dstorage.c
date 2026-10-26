#include <windows.h>

#define TOKEN_ENV "TLMP_SESSION_TOKEN"
#define CORE_DLL  L"%ProgramData%\\TrucklineMP\\trucklineets.dll"
#define SETTER    "tlmp_server_set_usr_sessiontoken"
#define RETRIES   100
#define RETRY_MS  50

typedef struct {
	char token[256];
} setter_args;

typedef DWORD(WINAPI *setter_fn)(setter_args *);

static setter_args g_args;
static setter_fn g_set;

// The setter returns a C++ bool, so only the low byte is valid.
static BOOL accepted(void)
{
	if ((g_set(&g_args) & 0xFF) == 0)
		return FALSE;
	SecureZeroMemory(&g_args, sizeof g_args);
	return TRUE;
}

static BOOL spawn(LPTHREAD_START_ROUTINE fn, LPVOID arg)
{
	HANDLE t = CreateThread(NULL, 0, fn, arg, 0, NULL);
	if (t)
		CloseHandle(t);
	return t != NULL;
}

static DWORD WINAPI fail_thread(LPVOID msg)
{
	SecureZeroMemory(&g_args, sizeof g_args);
	MessageBoxA(NULL, msg, "TrucklineMP", MB_OK | MB_ICONERROR);
	TerminateProcess(GetCurrentProcess(), 1);
	return 1;
}

// user32 must not run under the loader lock, so the dialog uses its own thread.
static void fail(const char *msg)
{
	if (!spawn(fail_thread, (LPVOID)msg))
		TerminateProcess(GetCurrentProcess(), 1);
}

static DWORD WINAPI retry_thread(LPVOID unused)
{
	(void)unused;
	for (int i = 0; i < RETRIES; i++) {
		Sleep(RETRY_MS);
		if (accepted())
			return 0;
	}
	return fail_thread("TrucklineMP did not accept the session token.");
}

static void attach(void)
{
	DWORD n = GetEnvironmentVariableA(TOKEN_ENV, g_args.token, sizeof g_args.token);
	SetEnvironmentVariableA(TOKEN_ENV, NULL);
	if (n == 0)
		return;
	if (n >= sizeof g_args.token) {
		fail("The TrucklineMP session token is too long.");
		return;
	}

	WCHAR path[MAX_PATH];
	DWORD len = ExpandEnvironmentStringsW(CORE_DLL, path, MAX_PATH);
	HMODULE core = len && len <= MAX_PATH ? LoadLibraryW(path) : NULL;
	if (!core) {
		fail("TrucklineMP is not installed. Run: tlmp-manager setup");
		return;
	}
	g_set = (setter_fn)GetProcAddress(core, SETTER);
	if (!g_set) {
		fail("This TrucklineMP version has no " SETTER " export.");
		return;
	}
	if (!accepted() && !spawn(retry_thread, NULL))
		fail("TrucklineMP did not accept the session token.");
}

BOOL WINAPI DllMain(HINSTANCE inst, DWORD reason, LPVOID reserved)
{
	(void)reserved;
	if (reason == DLL_PROCESS_ATTACH) {
		DisableThreadLibraryCalls(inst);
		attach();
	}
	return TRUE;
}
