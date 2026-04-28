use crate::db::models::{CodeElement, Relationship};
use regex::Regex;

pub struct TerraformExtractor {
    source: Vec<u8>,
    file_path: String,
}

impl TerraformExtractor {
    pub fn new(source: &[u8], file_path: &str) -> Self {
        Self {
            source: source.to_vec(),
            file_path: file_path.to_string(),
        }
    }

    pub fn extract(&self) -> (Vec<CodeElement>, Vec<Relationship>) {
        let mut elements = Vec::new();
        let content = String::from_utf8_lossy(&self.source);
        let source_str = content.as_ref();

        let resource_regex = Regex::new(r#"(?m)^resource\s+"([^"]+)"\s+"([^"]+)""#).ok();
        let data_regex = Regex::new(r#"(?m)^data\s+"([^"]+)"\s+"([^"]+)""#).ok();
        let variable_regex = Regex::new(r#"(?m)^variable\s+"([^"]+)""#).ok();
        let output_regex = Regex::new(r#"(?m)^output\s+"([^"]+)""#).ok();
        let module_regex = Regex::new(r#"(?m)^module\s+"([^"]+)""#).ok();
        let provider_regex = Regex::new(r#"(?m)^provider\s+"([^"]+)""#).ok();

        elements.push(CodeElement {
            qualified_name: self.file_path.clone(),
            element_type: "terraform".to_string(),
            name: self
                .file_path
                .rsplit('/')
                .next()
                .unwrap_or(&self.file_path)
                .to_string(),
            file_path: self.file_path.clone(),
            line_start: 1,
            line_end: source_str.lines().count() as u32,
            language: "terraform".to_string(),
            parent_qualified: None,
            metadata: serde_json::json!({}),
            ..Default::default()
        });

        if let Some(re) = resource_regex {
            for cap in re.captures_iter(source_str) {
                if let (Some(resource_type), Some(name)) = (cap.get(1), cap.get(2)) {
                    let qualified_name = format!("{}::{}", self.file_path, name.as_str());
                    elements.push(CodeElement {
                        qualified_name: qualified_name.clone(),
                        element_type: "terraform_resource".to_string(),
                        name: name.as_str().to_string(),
                        file_path: self.file_path.clone(),
                        line_start: source_str[..cap.get(0).unwrap().start()].lines().count()
                            as u32
                            + 1,
                        line_end: source_str[..cap.get(0).unwrap().start()].lines().count() as u32
                            + 1,
                        language: "terraform".to_string(),
                        parent_qualified: Some(self.file_path.clone()),
                        metadata: serde_json::json!({
                            "resource_type": resource_type.as_str(),
                            "name": name.as_str(),
                        }),
                        ..Default::default()
                    });
                }
            }
        }

        if let Some(re) = data_regex {
            for cap in re.captures_iter(source_str) {
                if let (Some(data_type), Some(name)) = (cap.get(1), cap.get(2)) {
                    let qualified_name = format!("{}::{}", self.file_path, name.as_str());
                    elements.push(CodeElement {
                        qualified_name: qualified_name.clone(),
                        element_type: "terraform_data".to_string(),
                        name: name.as_str().to_string(),
                        file_path: self.file_path.clone(),
                        line_start: source_str[..cap.get(0).unwrap().start()].lines().count()
                            as u32
                            + 1,
                        line_end: source_str[..cap.get(0).unwrap().start()].lines().count() as u32
                            + 1,
                        language: "terraform".to_string(),
                        parent_qualified: Some(self.file_path.clone()),
                        metadata: serde_json::json!({
                            "data_type": data_type.as_str(),
                            "name": name.as_str(),
                        }),
                        ..Default::default()
                    });
                }
            }
        }

        if let Some(re) = variable_regex {
            for cap in re.captures_iter(source_str) {
                if let Some(name) = cap.get(1) {
                    let qualified_name = format!("{}::var.{}", self.file_path, name.as_str());
                    elements.push(CodeElement {
                        qualified_name: qualified_name.clone(),
                        element_type: "terraform_variable".to_string(),
                        name: name.as_str().to_string(),
                        file_path: self.file_path.clone(),
                        line_start: source_str[..cap.get(0).unwrap().start()].lines().count()
                            as u32
                            + 1,
                        line_end: source_str[..cap.get(0).unwrap().start()].lines().count() as u32
                            + 1,
                        language: "terraform".to_string(),
                        parent_qualified: Some(self.file_path.clone()),
                        metadata: serde_json::json!({
                            "name": name.as_str(),
                        }),
                        ..Default::default()
                    });
                }
            }
        }

        if let Some(re) = output_regex {
            for cap in re.captures_iter(source_str) {
                if let Some(name) = cap.get(1) {
                    let qualified_name = format!("{}::out.{}", self.file_path, name.as_str());
                    elements.push(CodeElement {
                        qualified_name: qualified_name.clone(),
                        element_type: "terraform_output".to_string(),
                        name: name.as_str().to_string(),
                        file_path: self.file_path.clone(),
                        line_start: source_str[..cap.get(0).unwrap().start()].lines().count()
                            as u32
                            + 1,
                        line_end: source_str[..cap.get(0).unwrap().start()].lines().count() as u32
                            + 1,
                        language: "terraform".to_string(),
                        parent_qualified: Some(self.file_path.clone()),
                        metadata: serde_json::json!({
                            "name": name.as_str(),
                        }),
                        ..Default::default()
                    });
                }
            }
        }

        if let Some(re) = module_regex {
            for cap in re.captures_iter(source_str) {
                if let Some(name) = cap.get(1) {
                    let qualified_name = format!("{}::module.{}", self.file_path, name.as_str());
                    elements.push(CodeElement {
                        qualified_name: qualified_name.clone(),
                        element_type: "terraform_module".to_string(),
                        name: name.as_str().to_string(),
                        file_path: self.file_path.clone(),
                        line_start: source_str[..cap.get(0).unwrap().start()].lines().count()
                            as u32
                            + 1,
                        line_end: source_str[..cap.get(0).unwrap().start()].lines().count() as u32
                            + 1,
                        language: "terraform".to_string(),
                        parent_qualified: Some(self.file_path.clone()),
                        metadata: serde_json::json!({
                            "name": name.as_str(),
                        }),
                        ..Default::default()
                    });
                }
            }
        }

        if let Some(re) = provider_regex {
            for cap in re.captures_iter(source_str) {
                if let Some(name) = cap.get(1) {
                    let qualified_name = format!("{}::provider.{}", self.file_path, name.as_str());
                    elements.push(CodeElement {
                        qualified_name: qualified_name.clone(),
                        element_type: "terraform_provider".to_string(),
                        name: name.as_str().to_string(),
                        file_path: self.file_path.clone(),
                        line_start: source_str[..cap.get(0).unwrap().start()].lines().count()
                            as u32
                            + 1,
                        line_end: source_str[..cap.get(0).unwrap().start()].lines().count() as u32
                            + 1,
                        language: "terraform".to_string(),
                        parent_qualified: Some(self.file_path.clone()),
                        metadata: serde_json::json!({
                            "name": name.as_str(),
                        }),
                        ..Default::default()
                    });
                }
            }
        }

        (elements, Vec::new())
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn test_extract_terraform_resource() {
        let source = br#"
resource "aws_s3_bucket" "example" {
  bucket = "my-example-bucket"
}

resource "aws_ec2_instance" "web" {
  ami           = "ami-12345678"
  instance_type = "t2.micro"
}
"#;
        let extractor = TerraformExtractor::new(source, "main.tf");
        let (elements, _) = extractor.extract();

        let resources: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "terraform_resource")
            .collect();
        assert_eq!(resources.len(), 2);
    }

    #[test]
    fn test_extract_terraform_variable() {
        let source = br#"
variable "region" {
  type    = string
  default = "us-west-2"
}

variable "instance_type" {
  type    = string
  default = "t2.micro"
}
"#;
        let extractor = TerraformExtractor::new(source, "vars.tf");
        let (elements, _) = extractor.extract();

        let variables: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "terraform_variable")
            .collect();
        assert_eq!(variables.len(), 2);
    }

    #[test]
    fn test_extract_terraform_output() {
        let source = br#"
output "bucket_name" {
  value = aws_s3_bucket.example.bucket
}
"#;
        let extractor = TerraformExtractor::new(source, "outputs.tf");
        let (elements, _) = extractor.extract();

        let outputs: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "terraform_output")
            .collect();
        assert_eq!(outputs.len(), 1);
    }

    #[test]
    fn test_extract_terraform_data() {
        let source = br#"
data "aws_ami" "ubuntu" {
  most_recent = true
  owners      = ["099720109477"]
}
"#;
        let extractor = TerraformExtractor::new(source, "data.tf");
        let (elements, _) = extractor.extract();

        let data_sources: Vec<_> = elements
            .iter()
            .filter(|e| e.element_type == "terraform_data")
            .collect();
        assert_eq!(data_sources.len(), 1);
    }
}
