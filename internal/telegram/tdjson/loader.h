#ifndef TELECLI_TDJSON_LOADER_H
#define TELECLI_TDJSON_LOADER_H
#include <stddef.h>
typedef struct telecli_tdjson telecli_tdjson;
telecli_tdjson *telecli_tdjson_open(const char *path, char *err, size_t err_len);
int telecli_tdjson_create_client_id(telecli_tdjson *lib);
int telecli_tdjson_send(telecli_tdjson *lib, int client_id, const char *request, char *err, size_t err_len);
const char *telecli_tdjson_receive(telecli_tdjson *lib, double timeout, char *err, size_t err_len);
const char *telecli_tdjson_execute(telecli_tdjson *lib, const char *request, char *err, size_t err_len);
void telecli_tdjson_close(telecli_tdjson *lib);
#endif
