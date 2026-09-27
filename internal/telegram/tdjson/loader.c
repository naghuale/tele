//go:build cgo && (darwin || linux)

#include "loader.h"
#include <dlfcn.h>
#include <stdio.h>
#include <stdlib.h>
#include <string.h>

typedef int (*create_client_id_fn)(void);
typedef void (*send_fn)(int, const char *);
typedef const char *(*receive_fn)(double);
typedef const char *(*execute_fn)(const char *);

struct telecli_tdjson {
    void *handle;
    create_client_id_fn create_client_id;
    send_fn send;
    receive_fn receive;
    execute_fn execute;
};

static void set_error(char *err, size_t err_len, const char *message) {
    if (!err || err_len == 0) return;
    snprintf(err, err_len, "%s", message ? message : "unknown dynamic loader error");
}

static void *required_symbol(void *handle, const char *name, char *err, size_t err_len) {
    dlerror();
    void *symbol = dlsym(handle, name);
    const char *message = dlerror();
    if (message) { set_error(err, err_len, message); return NULL; }
    return symbol;
}

telecli_tdjson *telecli_tdjson_open(const char *path, char *err, size_t err_len) {
    const char *name = path && path[0] ? path :
#if defined(__APPLE__)
        "libtdjson.dylib";
#else
        "libtdjson.so";
#endif
    void *handle = dlopen(name, RTLD_NOW | RTLD_LOCAL);
    if (!handle) { set_error(err, err_len, dlerror()); return NULL; }
    telecli_tdjson *lib = calloc(1, sizeof(*lib));
    if (!lib) { set_error(err, err_len, "calloc failed"); dlclose(handle); return NULL; }
    lib->handle = handle;
    lib->create_client_id = (create_client_id_fn)required_symbol(handle, "td_create_client_id", err, err_len);
    lib->send = (send_fn)required_symbol(handle, "td_send", err, err_len);
    lib->receive = (receive_fn)required_symbol(handle, "td_receive", err, err_len);
    lib->execute = (execute_fn)required_symbol(handle, "td_execute", err, err_len);
    if (!lib->create_client_id || !lib->send || !lib->receive || !lib->execute) {
        dlclose(handle); free(lib); return NULL;
    }
    return lib;
}

int telecli_tdjson_create_client_id(telecli_tdjson *lib) { return lib->create_client_id(); }
int telecli_tdjson_send(telecli_tdjson *lib, int client_id, const char *request, char *err, size_t err_len) {
    if (!lib || !request) { set_error(err, err_len, "invalid td_send arguments"); return -1; }
    lib->send(client_id, request); return 0;
}
const char *telecli_tdjson_receive(telecli_tdjson *lib, double timeout, char *err, size_t err_len) {
    if (!lib) { set_error(err, err_len, "invalid td_receive handle"); return NULL; }
    return lib->receive(timeout);
}
const char *telecli_tdjson_execute(telecli_tdjson *lib, const char *request, char *err, size_t err_len) {
    if (!lib || !request) { set_error(err, err_len, "invalid td_execute arguments"); return NULL; }
    return lib->execute(request);
}
/*
 * The library handle is deliberately never passed to dlclose. TDLib keeps
 * process-wide worker threads and static state alive after every client
 * has closed, and unloading its code under those threads can crash the
 * process on exit. A later open of the same path gets the already loaded
 * image back, so a second runtime still initializes normally.
 */
void telecli_tdjson_close(telecli_tdjson *lib) {
    if (!lib) return;
    free(lib);
}
