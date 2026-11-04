// Call flow of the resource operations (the package doc is in env.go):
//
//	resource add|edit ──► kindDef (kinds + kind_fields rows)
//	                      └► applySets ─► fieldValue ─► parseAs / ref / secretRefValue(kind secret_manager)
//	                                                  └► checkFieldRules (pattern, min, max; logic/meta)
//	                      └► applyDefaults (add) ─► requireFields ─► valuesPatch ─► Env.Create|Update
//	                      └► deprecationNotes (warning lines)
//	resource graph|impact ─► buildIndex (every ref field of every kind) ─► walk (BFS hops + DFS tree)
//	                         impact ─► openLinks (knowledge_refs to open tasks / incidents)
//	resource delete --with-dependents ─► impact set ─► bulk.Ask ─► one delete batch
//	resource rename|merge|lint, field retype ─► resources_maint.go header

package settings
