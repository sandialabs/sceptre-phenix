This `scorchmd` package exists as a separate package so both the `api/scorch`
and `web/scorch` packages can use the methods provided here for parsing SCORCH
app metadata.

Since `api/scorch` has to call `web/scorch` to update the UI pipeline and create
web terminals, if this were in the main `api/scorch` package then there would be
an import loop.