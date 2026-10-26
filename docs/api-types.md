# Public API type investigation — 2026-10-26

## Sources and method

The official [Minquan appointment information page](https://www.dianthus.com.tw/MC/ugC_Appointments.asp)
links to the [public medical-progress page](https://web.dianthus.com.tw/medical-progress).
The progress page rendered public queue information without signing in.
The available browser interface exposed DOM/script references and console logs,
but no network request inspector. Findings below are public source evidence,
not a claim of captured network traffic.

The page loaded [app.74dd9546d5b60e127ac6.js](https://web.dianthus.com.tw/app.74dd9546d5b60e127ac6.js),
which explicitly references its [public source map](https://web.dianthus.com.tw/app.74dd9546d5b60e127ac6.js.map).
Bundle SHA-256: `c27f43430ef698c7e0c0f6542dd1c7958ac5533b13c13ebc441317ca12ff1d65`.
No third-party source or real queue records are vendored here.

## Confirmed in public source

`src/models/departmentRegistration.ts`, lines 119 and 126, declares the query
mode union as `BRANCH | DEPARTMENT` and builds the query with `branch_id` for
BRANCH, otherwise `department_id`. The same mapping exists in
`src/models/doctorDepartmentRegistration.ts`. The generated API wrapper in
`src/util/api/swaggerApi/Api.ts` uses GET `/api/v1/hospital/branche-department-ref`.

BRANCH mode reads the first result's nested `departments`; DEPARTMENT mode
uses returned branch IDs to filter available branches. These describe frontend
expectations, not a newly validated server response contract.

`src/models/medicalProgress.ts` calls `v1MedicalProgress`; its wrapper uses
GET `/api/v1/hospital/branche/{id}/clinic-progress`. Keep the existing CLI/MCP route.
The older `/api/v1/hospital/clinic-progress` appears only as a generated method
in this bundle, without a call site or included query-type definition. Its
`type` enum remains unresolved. Values from the other endpoint are not evidence
for this endpoint.

## Final bounded source review

The already observed [runtime manifest](https://web.dianthus.com.tw/runtime.ba5e163283f415d1bc97.js)
resolves the public medical-progress route's `Login` chunk to
[Login.bba2284df49130186c8c.chunk.js](https://web.dianthus.com.tw/Login.bba2284df49130186c8c.chunk.js).
Its explicitly referenced [source map](https://web.dianthus.com.tw/Login.bba2284df49130186c8c.chunk.js.map)
contains `src/layouts/MedicalProgress/index.tsx`, whose search handler calls
`getMedicalProgressList`. Neither this chunk/map nor the main bundle/map contains
an actual call site for `v1HospitalClinicProgressList` or the declaration of
`V1HospitalClinicProgressListRequestParams`. The generated API file references
that type, but its definition is not included in these maps.

This bounded review does not establish that the enum is absent from every
frontend version or other application. The uppercase BRANCH/DEPARTMENT values
are confirmed frontend inputs only; server acceptance and case sensitivity
were not verified. No denied API request was retried during this source review.

## Bounded validation and limitation

A credential-free GET to
`https://api.dianthus.com.tw/api/v1/hospital/branche-department-ref?type=BRANCH&branch_id=1`
returned HTTP 403. No response schema was established. The validation stopped;
the DEPARTMENT example was not requested, and no guessed values, authentication,
patient records, booking actions or restriction workarounds were used.

No new CLI/MCP command is exposed without a verified response contract.
An offline regression test asserts that existing progress requests retain the
public dashboard path, use GET, and do not add a `type` query parameter.
