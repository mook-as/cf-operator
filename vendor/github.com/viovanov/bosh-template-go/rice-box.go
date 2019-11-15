package boshgotemplate

import (
	"time"

	"github.com/GeertJohan/go.rice/embedded"
)

func init() {

	// define files
	file2 := &embedded.EmbeddedFile{
		Filename:    "template_evaluation_context.rb",
		FileModTime: time.Unix(1564663150, 0),

		Content: string("require \"bosh/template\"\nrequire \"erb\"\nrequire \"fileutils\"\nrequire \"json\"\nrequire \"yaml\"\n\nif $0 == __FILE__\n  context_path, spec_path, instance_path, src_path, dst_path = *ARGV\n\n  puts \"Context file: #{context_path}\"\n  puts \"Instance file: #{instance_path}\"\n  puts \"Spec file: #{spec_path}\"\n  puts \"Template file: #{src_path}\"\n  puts \"Output file: #{dst_path}\"\n\n  # Load the context hash\n  context_hash = YAML.load_file(context_path)\n\n  # Load the job spec\n  job_spec = YAML.load_file(spec_path)\n  job_spec['properties'] = {} if job_spec['properties'].nil?\n\n  # Load the instace info\n  instance_info = YAML.load_file(instance_path)\n\n  # Read the erb template\n  begin\n    perms = File.stat(src_path).mode\n    template = Bosh::Template::Test::Template.new(job_spec, src_path)\n  rescue Errno::ENOENT\n    raise \"failed to read template file #{src_path}\"\n  end\n\n  # Build links\n  links = []\n  if context_hash['properties'] && context_hash['properties']['quarks'] && context_hash['properties']['quarks']['consumes']\n    context_hash['properties']['quarks']['consumes'].each_pair do |name, link|\n      next if link['instances'].empty?\n\n      instances = []\n      link['instances'].each do |link_instance|\n        instances << Bosh::Template::Test::InstanceSpec.new(\n          address:   link_instance['address'],\n          az:        link_instance['az'],\n          bootstrap: link_instance['bootstrap'],\n          id:        link_instance['id'],\n          index:     link_instance['index'],\n          name:      link_instance['name'],\n        )\n      end\n      links << Bosh::Template::Test::Link.new(\n        name: name,\n        address: link['address'],\n        instances: instances,\n        properties: link['properties'],\n      )\n    end\n  end\n\n  # Build instance\n  instance = Bosh::Template::Test::InstanceSpec.new(\n    address:    instance_info['address'],\n    az:         instance_info['az'],\n    bootstrap:  instance_info['bootstrap'],\n    deployment: instance_info['deployment'],\n    id:         instance_info['id'],\n    index:      instance_info['index'],\n    ip:         instance_info['ip'],\n    name:       instance_info['name'],\n    networks:   {'default' => {'ip' => instance_info['ip'],\n                               'dns_record_name' => instance_info['address'],\n                               # TODO: Do we need more, like netmask and gateway?\n                               # https://github.com/cloudfoundry/bosh-agent/blob/master/agent/applier/applyspec/v1_apply_spec_test.go\n                              }},\n  )\n\n  # Process the Template\n  output = template.render(context_hash['properties'], spec: instance, consumes: links)\n\n  begin\n    # Open the output file\n    output_dir = File.dirname(dst_path)\n    FileUtils.mkdir_p(output_dir)\n    out_file = File.open(dst_path, 'w')\n\n    # Write results to the output file\n    out_file.write(output)\n\n    # Set the appropriate permissions on the output file\n    if File.basename(File.dirname(dst_path)) == 'bin'\n      out_file.chmod(0755)\n    else\n      out_file.chmod(perms)\n    end\n  rescue Errno::ENOENT, Errno::EACCES => e\n    out_file = nil\n    raise \"failed to open output file #{dst_path}: #{e}\"\n  ensure\n    out_file.close unless out_file.nil?\n  end\nend\n"),
	}

	// define dirs
	dir1 := &embedded.EmbeddedDir{
		Filename:   "",
		DirModTime: time.Unix(1564663129, 0),
		ChildFiles: []*embedded.EmbeddedFile{
			file2, // "template_evaluation_context.rb"

		},
	}

	// link ChildDirs
	dir1.ChildDirs = []*embedded.EmbeddedDir{}

	// register embeddedBox
	embedded.RegisterEmbeddedBox(`rb`, &embedded.EmbeddedBox{
		Name: `rb`,
		Time: time.Unix(1564663129, 0),
		Dirs: map[string]*embedded.EmbeddedDir{
			"": dir1,
		},
		Files: map[string]*embedded.EmbeddedFile{
			"template_evaluation_context.rb": file2,
		},
	})
}
